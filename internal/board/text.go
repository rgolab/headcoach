package board

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rouxlabs/headcoach/internal/store"
)

var (
	jiraKeyRe = regexp.MustCompile(`\b[A-Z][A-Z0-9]+-\d+\b`)
	// A merge request is written !123. The character before the bang must not glue it to a word or a path.
	mrRefRe      = regexp.MustCompile(`(?:^|[^\pL\pN_/!])!(\d+)\b`)
	sshRemoteRe  = regexp.MustCompile(`^(?:ssh://)?git@([^:/]+)(?::\d+)?[:/](.+?)(?:\.git)?/?$`)
	httpRemoteRe = regexp.MustCompile(`^(https?://[^/@]+/.+?)(?:\.git)?/?$`)
	wordRe       = regexp.MustCompile(`[a-z0-9]+`)
	leadMarksRe  = regexp.MustCompile(`^[-#>\s]+`)
	markupRe     = regexp.MustCompile("[*`#]+")
	// askRe matches a sentence that asks you for a go, a pick or a yes without a question mark: Reply "go", Pick one.
	askRe = regexp.MustCompile(`(?i)(^|[.!?:;,][*_]*\s+|^\d+[.)]\s+)[*_]*(just\s+)?(` +
		`(reply|say|answer)(\s+with)?\s+["'“‘*_` + "`" + `]*(go|yes|ok|no)["'”’*_` + "`" + `]*(\s+(and I|to|or)\b|\s*,|[.!]*$)` +
		`|(pick|choose)\s+(one|which|between)\b|(confirm|approve)\s+(and|or|to)\b)`)
	// waitRe matches a line that ends on what waits for you: That is your call. Waiting for your go.
	waitRe = regexp.MustCompile(`(?i)\b((for|is|awaits|needs?) your (go|call|pick|yes|ok)|waiting (on|for) you(r (go|answer|reply|call|pick))?)[*_.!]*$`)
	// notRe keeps a negated clause, such as Nothing is waiting on you, from reading as an ask.
	notRe = regexp.MustCompile(`(?i)\b(nothing|nobody|no one|not|no longer|never)\b`)
)

// Clip squeezes whitespace and cuts the text to n characters, ending on an ellipsis when it was cut.
func Clip(text string, n int) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) <= n {
		return text
	}
	return strings.TrimRightFunc(string(runes[:n-1]), isSpace) + "…"
}

func isSpace(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }

// lines splits on any line ending.
func lines(text string) []string {
	return strings.Split(strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(text), "\n")
}

// FirstLine returns the first line that is not blank, trimmed.
func FirstLine(text string) string {
	for _, line := range lines(text) {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// TitleFromPrompt names a task after the first line of its first prompt.
func TitleFromPrompt(prompt string) string {
	if title := Clip(FirstLine(prompt), 72); title != "" {
		return title
	}
	return "Untitled session"
}

// JiraKey is the Jira key a task is about, taken from its title, prompt or branch name.
func JiraKey(t *store.Task) string {
	return jiraKeyRe.FindString(t.Title + " " + t.Prompt + " " + t.Branch)
}

// MRNumber is the merge request a task mentions as !123, without the bang.
func MRNumber(t *store.Task) string {
	if found := mrRefRe.FindStringSubmatch(t.Title + " " + t.Prompt); found != nil {
		return found[1]
	}
	return ""
}

// RemoteWebURL turns a git remote into the project's web address. Empty when it is not a recognisable host.
func RemoteWebURL(remote string) string {
	remote = strings.TrimSpace(remote)
	if found := sshRemoteRe.FindStringSubmatch(remote); found != nil {
		return "https://" + found[1] + "/" + found[2]
	}
	if found := httpRemoteRe.FindStringSubmatch(remote); found != nil {
		return found[1]
	}
	return ""
}

// Short is the first 8 characters of an id, the form people type.
func Short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// WindowName names a task's tmux window: the short task id, then its Jira key or a few words of its title.
func WindowName(t *store.Task) string {
	short := Short(t.ID)
	if key := JiraKey(t); key != "" {
		return short + "-" + key
	}
	words := wordRe.FindAllString(strings.ToLower(t.Title), 3)
	if len(words) == 0 {
		return short
	}
	slug := strings.Join(words, "-")
	if len(slug) > 24 {
		slug = slug[:24]
	}
	return short + "-" + strings.TrimRight(slug, "-")
}

// closingLine returns the last line of a message without its list marks and outer markup.
func closingLine(message string) string {
	last := ""
	for _, line := range lines(message) {
		if line = strings.TrimSpace(line); line != "" {
			last = line
		}
	}
	return strings.Trim(leadMarksRe.ReplaceAllString(last, ""), "*_` ")
}

// ClosingQuestion returns the last line when the agent ended its turn on a question, else an empty string.
func ClosingQuestion(message string) string {
	last := closingLine(message)
	if !strings.HasSuffix(last, "?") {
		return ""
	}
	return Clip(last, 300)
}

// ClosingAsk returns the last line when it asks for your go without a question mark, such as `Reply "go"`, else
// an empty string.
func ClosingAsk(message string) string {
	last := closingLine(message)
	// A negation counts only in the clause that waits: Not urgent, it waits for your go.
	clause := last[strings.LastIndexAny(last, ",;")+1:]
	if !askRe.MatchString(last) && (!waitRe.MatchString(last) || notRe.MatchString(clause)) {
		return ""
	}
	return Clip(last, 300)
}

// SummarizeTool describes one tool call in a line. input is the decoded tool_input, of any shape.
func SummarizeTool(tool string, input any) string {
	ti := map[string]string{}
	if m, ok := input.(map[string]any); ok {
		for key, value := range m {
			if s, ok := value.(string); ok {
				ti[key] = s
			}
		}
	}
	first := func(keys ...string) string {
		for _, key := range keys {
			if ti[key] != "" {
				return ti[key]
			}
		}
		return ""
	}
	switch tool {
	case "Bash":
		return Clip("$ "+FirstLine(ti["command"]), 100)
	case "Read", "Edit", "Write", "NotebookEdit":
		path := first("file_path", "notebook_path")
		if path != "" {
			path = filepath.Base(path)
		}
		return Clip(tool+" "+path, 100)
	case "Grep", "Glob":
		return Clip(tool+" "+ti["pattern"], 100)
	case "WebFetch", "WebSearch":
		return Clip(tool+" "+first("url", "query"), 100)
	case "Agent", "Task":
		return Clip(strings.TrimRight("Agent "+ti["subagent_type"]+": "+ti["description"], ": "), 100)
	case "Skill":
		return Clip("Skill "+ti["skill"], 100)
	}
	if strings.HasPrefix(tool, "mcp__") {
		if parts := strings.Split(tool, "__"); len(parts) > 2 {
			return Clip(parts[1]+": "+parts[len(parts)-1], 100)
		}
		return Clip(tool, 100)
	}
	return tool
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// git runs one git command in cwd. Empty on any failure: git information is a nicety, never a requirement.
func git(cwd string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", cwd, "--no-optional-locks"}, args...)...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// GitInfo returns the repo name and the branch of a directory. Empty strings when it does not exist.
func GitInfo(cwd string) (repo, branch string) {
	if cwd == "" || !isDir(cwd) {
		return "", ""
	}
	top := git(cwd, "rev-parse", "--show-toplevel")
	if top == "" {
		return filepath.Base(strings.TrimRight(cwd, "/")), ""
	}
	if branch = git(cwd, "branch", "--show-current"); branch == "" {
		branch = git(cwd, "rev-parse", "--short", "HEAD")
	}
	return filepath.Base(top), branch
}

// GitHead is the commit a directory's checkout is on. Empty outside a repository or before its first commit.
func GitHead(cwd string) string {
	if cwd == "" || !isDir(cwd) {
		return ""
	}
	return git(cwd, "rev-parse", "--verify", "-q", "HEAD")
}

// GitRemote is the web address of the project a checkout belongs to.
// `upstream` wins: that is where merge requests live.
func GitRemote(cwd string) string {
	if cwd == "" || !isDir(cwd) {
		return ""
	}
	for _, name := range []string{"upstream", "origin"} {
		if url := RemoteWebURL(git(cwd, "remote", "get-url", name)); url != "" {
			return url
		}
	}
	return ""
}

// NewID returns a random UUID, the form Claude Code uses for session ids.
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // the system has no randomness: nothing sensible can follow
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
