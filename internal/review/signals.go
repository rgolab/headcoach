// Package review gathers what the review queue shows about a task's change: what its transcripts say
// about tests, pushes, pull requests and reviewers, and what its git checkout says about the change itself.
//
// Transcripts are read incrementally, as in package usage: each call only parses what was appended
// since the last one. The format belongs to Claude Code, so only the few fields needed are read.
package review

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
)

// Test results.
const (
	Passed = "passed"
	Failed = "failed"
)

var (
	// A push of any kind: a branch, main or a tag. `git -C dir push` counts too.
	pushRe = regexp.MustCompile(`(^|[;&|(\s])git(\s+-C\s+\S+)?\s+push\b`)
	// A command that opens a pull or merge request: it prints the address of the new one.
	openRe = regexp.MustCompile(`\b(gh\s+pr\s+create|glab\s+mr\s+create)\b`)
	// A pull request on GitHub or Gitea, or a merge request on GitLab.
	prRe = regexp.MustCompile(`https?://[^\s"'<>()\[\]` + "`" + `]+/(?:pull|-/merge_requests)/\d+`)
	// Output of a test run that failed, for a command piped into tail or tee, which hides the exit status.
	failRe = regexp.MustCompile(`(?m)^(FAIL\b|--- FAIL|make(\[\d+\])?: \*\*\*|npm ERR!|FAILED\b)|\b[1-9]\d* (failed|failures?)\b`)
	// Output of a command sent to the background: it has not finished.
	backgroundPrefix = "Command running in background"
)

// Signals is what a task's transcripts say about its change.
type Signals struct {
	// Tests is Passed or Failed for the last test command, empty when none ran.
	Tests   string
	TestCmd string
	testsAt string
	// Pushed tells a git push succeeded.
	Pushed bool
	// PRs are the pull and merge request links, oldest first, each once.
	PRs []string
	// Opened are the pull and merge requests the task opened itself.
	Opened []string
	// LastText is the last thing the agent wrote.
	LastText string
}

// merge adds what another transcript of the same task says: the latest of each wins.
func (s *Signals) merge(o Signals) {
	if o.testsAt > s.testsAt {
		s.Tests, s.TestCmd, s.testsAt = o.Tests, o.TestCmd, o.testsAt
	}
	s.Pushed = s.Pushed || o.Pushed
	for _, url := range o.PRs {
		s.PRs = addPR(s.PRs, url)
	}
	for _, url := range o.Opened {
		s.Opened = addPR(s.Opened, url)
	}
}

// OpenedPR tells whether the task opened that pull or merge request itself.
func (s Signals) OpenedPR(url string) bool {
	for _, opened := range s.Opened {
		if opened == url {
			return true
		}
	}
	return false
}

// PR is the pull or merge request of the project at remote the task is about: the last one it opened,
// or else the last one it mentions. Any project's counts when remote is empty.
func (s Signals) PR(remote string) string {
	if url := last(s.Opened, remote); url != "" {
		return url
	}
	return last(s.PRs, remote)
}

func last(list []string, remote string) string {
	for i := len(list) - 1; i >= 0; i-- {
		if remote == "" || strings.HasPrefix(list[i], strings.TrimRight(remote, "/")+"/") {
			return list[i]
		}
	}
	return ""
}

// TestMatcher compiles the pattern of the test commands so it only matches where a command starts:
// at the start of the line or after ;, &&, || or a pipe, behind variables such as TMPDIR=/tmp. A
// command that only mentions one, such as grep "go test", is not a test run. Nil for an empty pattern.
func TestMatcher(pattern string) (*regexp.Regexp, error) {
	if strings.TrimSpace(pattern) == "" {
		return nil, nil
	}
	return regexp.Compile(`(^|[;&|(\n]\s*)([A-Za-z_][A-Za-z0-9_]*=\S*\s+)*(?:` + pattern + `)`)
}

func addPR(list []string, url string) []string {
	for i, known := range list {
		if known == url {
			list = append(list[:i:i], list[i+1:]...)
			break
		}
	}
	return append(list, url)
}

// call is a shell command the agent ran, waiting for its result.
type call struct {
	command  string
	test     bool
	push     bool
	opens    bool
	at       string
	resolved bool
}

type file struct {
	offset  int64
	signals Signals
	calls   map[string]*call
}

type line struct {
	Type        string          `json:"type"`
	Timestamp   string          `json:"timestamp"`
	DenialKind  string          `json:"toolDenialKind"`
	IsSidechain bool            `json:"isSidechain"`
	Message     json.RawMessage `json:"message"`
}

type message struct {
	Content json.RawMessage `json:"content"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

// text reads a tool result's content: a string, or a list of text blocks.
func text(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []block
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var out []string
	for _, p := range parts {
		if p.Type == "text" {
			out = append(out, p.Text)
		}
	}
	return strings.Join(out, "\n")
}

func (f *file) read(path string, tests *regexp.Regexp) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if info.Size() < f.offset { // the file was replaced
		*f = file{}
	}
	if info.Size() == f.offset {
		return
	}
	fh, err := os.Open(path)
	if err != nil {
		return
	}
	defer fh.Close()
	if _, err := fh.Seek(f.offset, io.SeekStart); err != nil {
		return
	}
	chunk, err := io.ReadAll(fh)
	if err != nil {
		return
	}
	end := bytes.LastIndexByte(chunk, '\n') + 1 // a line still being written waits for the next call
	f.offset += int64(end)
	if f.calls == nil {
		f.calls = map[string]*call{}
	}
	for _, raw := range bytes.Split(chunk[:end], []byte("\n")) {
		f.line(raw, tests)
	}
}

func (f *file) line(raw []byte, tests *regexp.Regexp) {
	var l line
	if json.Unmarshal(raw, &l) != nil || len(l.Message) == 0 {
		return
	}
	var m message
	if json.Unmarshal(l.Message, &m) != nil {
		return
	}
	var blocks []block
	if json.Unmarshal(m.Content, &blocks) != nil {
		return
	}
	for _, b := range blocks {
		switch {
		case l.Type == "assistant" && b.Type == "text":
			if t := strings.TrimSpace(b.Text); t != "" {
				f.signals.LastText = t
			}
			f.links(b.Text)
		case l.Type == "assistant" && b.Type == "tool_use" && b.Name == "Bash":
			var input struct {
				Command string `json:"command"`
			}
			_ = json.Unmarshal(b.Input, &input)
			c := &call{command: input.Command, at: l.Timestamp}
			c.test = tests != nil && tests.MatchString(input.Command)
			c.push = pushRe.MatchString(input.Command)
			c.opens = openRe.MatchString(input.Command)
			if c.test || c.push || c.opens {
				f.calls[b.ID] = c
			}
		case l.Type == "user" && b.Type == "tool_result":
			out := text(b.Content)
			f.links(out)
			c := f.calls[b.ToolUseID]
			if c == nil || c.resolved || l.DenialKind != "" || strings.HasPrefix(out, backgroundPrefix) {
				continue
			}
			c.resolved = true
			delete(f.calls, b.ToolUseID)
			failed := b.IsError || failRe.MatchString(out)
			if c.test {
				f.signals.Tests, f.signals.TestCmd, f.signals.testsAt = Passed, c.command, c.at
				if failed {
					f.signals.Tests = Failed
				}
			}
			if c.push && !b.IsError {
				f.signals.Pushed = true
			}
			if c.opens && !b.IsError {
				for _, url := range prRe.FindAllString(out, -1) {
					f.signals.Opened = addPR(f.signals.Opened, url)
				}
			}
		}
	}
}

func (f *file) links(s string) {
	for _, url := range prRe.FindAllString(s, -1) {
		f.signals.PRs = addPR(f.signals.PRs, url)
	}
}

var (
	mu    sync.Mutex
	files = map[string]*file{}
)

// Of reads one transcript. tests matches the commands that run tests; nil matches none.
func Of(path string, tests *regexp.Regexp) Signals {
	if info, err := os.Stat(path); path == "" || err != nil || !info.Mode().IsRegular() {
		return Signals{}
	}
	key := path
	if tests != nil {
		key = tests.String() + "\x00" + path
	}
	mu.Lock()
	defer mu.Unlock()
	f := files[key]
	if f == nil {
		f = &file{}
		files[key] = f
	}
	f.read(path, tests)
	out := f.signals
	out.PRs = append([]string(nil), out.PRs...)
	out.Opened = append([]string(nil), out.Opened...)
	return out
}

// OfTask merges what several transcripts of one task say: its sessions and its sub-agents. A later
// path wins over an earlier one for the links: put the sessions after their sub-agents.
func OfTask(paths []string, tests *regexp.Regexp) Signals {
	var out Signals
	for _, path := range paths {
		out.merge(Of(path, tests))
	}
	return out
}

// verdictRe finds the line of a review that gives its verdict.
var verdictRe = regexp.MustCompile(`(?i)\b(verdict|approved?|lgtm|ship it|changes requested|request(ed)? changes|blocking|no (findings|issues)|[0-9]+ (findings?|issues?))\b`)

// Verdict is the gist of a reviewer's report: the line that gives its verdict, or its first line. A
// heading of one word, such as "## Verdict", gives the line under it.
func Verdict(report string) string {
	first, heading := "", false
	for _, l := range strings.Split(report, "\n") {
		l = strings.TrimSpace(strings.Trim(strings.TrimSpace(l), "#*_>-`:"))
		if l == "" {
			continue
		}
		if heading {
			return l
		}
		if first == "" {
			first = l
		}
		if verdictRe.MatchString(l) {
			if !strings.Contains(l, " ") {
				heading = true
				continue
			}
			return l
		}
	}
	return first
}
