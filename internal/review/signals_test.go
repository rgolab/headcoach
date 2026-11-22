package review

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/humblebundledore/headcoach/internal/config"
)

var testsRe, _ = TestMatcher(config.DefaultTestPattern)

// transcript writes the lines of a transcript, each one a JSON value, and returns its path.
func transcript(t *testing.T, dir, name string, lines ...any) string {
	t.Helper()
	var out []string
	for _, l := range lines {
		raw, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(raw))
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

type obj = map[string]any

func bash(id, at, command string) obj {
	return obj{"type": "assistant", "timestamp": at, "message": obj{"content": []obj{
		{"type": "tool_use", "id": id, "name": "Bash", "input": obj{"command": command}},
	}}}
}

func result(id, at, output string, isError bool) obj {
	return obj{"type": "user", "timestamp": at, "message": obj{"content": []obj{
		{"type": "tool_result", "tool_use_id": id, "content": output, "is_error": isError},
	}}}
}

func said(at, text string) obj {
	return obj{"type": "assistant", "timestamp": at, "message": obj{"content": []obj{{"type": "text", "text": text}}}}
}

func TestSignals(t *testing.T) {
	dir := t.TempDir()
	path := transcript(t, dir, "s.jsonl",
		bash("t1", "2026-01-01T10:00:00Z", "make check"),
		result("t1", "2026-01-01T10:01:00Z", "ok  ./...", false),
		bash("t2", "2026-01-01T10:02:00Z", "go test ./... 2>&1 | tail -5"),
		result("t2", "2026-01-01T10:03:00Z", "--- FAIL: TestX\nFAIL\tpkg", false),
		bash("p1", "2026-01-01T10:04:00Z", "git push origin main"),
		result("p1", "2026-01-01T10:04:30Z", "rejected", true),
		bash("p2", "2026-01-01T10:05:00Z", "git -C /x push"),
		result("p2", "2026-01-01T10:05:30Z", "main -> main", false),
		bash("l1", "2026-01-01T10:06:00Z", "ls"),
		result("l1", "2026-01-01T10:06:01Z", "Opened https://github.com/o/r/pull/7 and https://example.com/x", false),
		said("2026-01-01T10:07:00Z", "See https://gitlab.com/g/p/-/merge_requests/3 and https://github.com/o/r/pull/7."),
	)
	got := Of(path, testsRe)
	want := Signals{Tests: Failed, TestCmd: "go test ./... 2>&1 | tail -5", testsAt: "2026-01-01T10:02:00Z",
		Pushed: true, PRs: []string{"https://gitlab.com/g/p/-/merge_requests/3", "https://github.com/o/r/pull/7"},
		LastText: "See https://gitlab.com/g/p/-/merge_requests/3 and https://github.com/o/r/pull/7."}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("signals =\n%+v, want\n%+v", got, want)
	}
	if pr := got.PR("https://github.com/o/r"); pr != "https://github.com/o/r/pull/7" {
		t.Errorf("PR of the project = %q", pr)
	}
	if pr := got.PR("https://github.com/o/r2"); pr != "" {
		t.Errorf("PR of another project = %q", pr)
	}
	if pr := got.PR(""); pr != "https://github.com/o/r/pull/7" {
		t.Errorf("last PR = %q", pr)
	}
}

func TestSignalsSkipWhatDidNotRun(t *testing.T) {
	dir := t.TempDir()
	denied := result("t1", "2026-01-01T10:00:01Z", "blocked by a deny rule", true)
	denied["toolDenialKind"] = "permission-rule"
	path := transcript(t, dir, "s.jsonl",
		bash("t1", "2026-01-01T10:00:00Z", "make test"),
		denied,
		bash("t2", "2026-01-01T10:01:00Z", "pytest"),
		result("t2", "2026-01-01T10:01:01Z", "Command running in background with ID: b1", false),
		bash("t3", "2026-01-01T10:02:00Z", "make build"),
		result("t3", "2026-01-01T10:02:01Z", "", true),
	)
	if got := Of(path, testsRe); got.Tests != "" || got.Pushed {
		t.Errorf("signals = %+v, want none", got)
	}
}

func TestSignalsReadOnlyWhatWasAppended(t *testing.T) {
	dir := t.TempDir()
	path := transcript(t, dir, "s.jsonl", bash("t1", "2026-01-01T10:00:00Z", "make check"))
	if got := Of(path, testsRe); got.Tests != "" {
		t.Errorf("a test without its result = %q", got.Tests)
	}
	fh, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	raw, _ := json.Marshal(result("t1", "2026-01-01T10:01:00Z", "Exit code 2", true))
	fh.Write(append(raw, '\n'))
	fh.Close()
	if got := Of(path, testsRe); got.Tests != Failed {
		t.Errorf("after its result = %q", got.Tests)
	}
}

func TestOfTaskTakesTheLatest(t *testing.T) {
	dir := t.TempDir()
	main := transcript(t, dir, "main.jsonl",
		bash("t1", "2026-01-01T10:05:00Z", "make check"), result("t1", "2026-01-01T10:06:00Z", "ok", false))
	agent := transcript(t, dir, "agent.jsonl",
		bash("t1", "2026-01-01T10:00:00Z", "make check"), result("t1", "2026-01-01T10:01:00Z", "FAIL", true),
		bash("p1", "2026-01-01T10:02:00Z", "git push"), result("p1", "2026-01-01T10:03:00Z", "", false))
	got := OfTask([]string{main, agent, filepath.Join(dir, "missing.jsonl")}, testsRe)
	if got.Tests != Passed || !got.Pushed {
		t.Errorf("task signals = %+v", got)
	}
}

func TestVerdict(t *testing.T) {
	for report, want := range map[string]string{
		"":                                        "",
		"# Review of abc..def\n\nAll good.":       "Review of abc..def",
		"## Review\n\n**Verdict: approve**\n- x":  "Verdict: approve",
		"Found 2 findings:\n1. a\n2. b":           "Found 2 findings",
		"## Verdict\n\nApprove, 2 nits":           "Approve, 2 nits",
		"Looks fine\n\nNo findings in the range.": "No findings in the range.",
	} {
		if got := Verdict(report); got != want {
			t.Errorf("Verdict(%q) = %q, want %q", report, got, want)
		}
	}
}

func TestOpened(t *testing.T) {
	dir := t.TempDir()
	path := transcript(t, dir, "s.jsonl",
		said("2026-01-01T10:00:00Z", "Like https://github.com/o/r/pull/1"),
		bash("g1", "2026-01-01T10:01:00Z", "gh pr create --fill"),
		result("g1", "2026-01-01T10:01:30Z", "https://github.com/o/r/pull/2", false),
		bash("g2", "2026-01-01T10:02:00Z", "glab mr create -f"),
		result("g2", "2026-01-01T10:02:30Z", "failed: https://gitlab.com/g/p/-/merge_requests/9", true),
	)
	got := Of(path, nil)
	if !got.OpenedPR("https://github.com/o/r/pull/2") || got.OpenedPR("https://github.com/o/r/pull/1") ||
		got.OpenedPR("https://gitlab.com/g/p/-/merge_requests/9") {
		t.Errorf("opened = %v", got.Opened)
	}
}

func TestStateOf(t *testing.T) {
	saved := forge
	defer func() { forge = saved }()
	var calls []string
	forge = func(name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		switch {
		case name == "gh" && strings.Contains(args[2], "/1"):
			return []byte(`{"state": "OPEN", "headRefName": "feat"}`), nil
		case name == "gh":
			return []byte(`{"state": "MERGED", "headRefName": "feat"}`), nil
		case name == "glab":
			return []byte(`{"state": "opened", "source_branch": "fix"}`), nil
		}
		return nil, os.ErrNotExist
	}
	for url, want := range map[string]PR{
		"https://github.com/o/r/pull/1":             {State: "open"},
		"https://github.com/o/r/pull/2":             {State: "merged"},
		"https://gitlab.com/g/p/-/merge_requests/3": {State: "open"},
	} {
		if got := StateOf(url); got != want {
			t.Errorf("StateOf(%s) = %+v, want %+v", url, got, want)
		}
	}
	n := len(calls)
	StateOf("https://github.com/o/r/pull/2")
	StateOf("https://github.com/o/r/pull/1")
	if len(calls) != n {
		t.Errorf("the forge was asked again: %v", calls[n:])
	}
	forge = func(string, ...string) ([]byte, error) { return nil, os.ErrNotExist }
	if got := StateOf("https://github.com/o/r/pull/99"); got != (PR{}) {
		t.Errorf("without gh = %+v", got)
	}
}

func TestTestMatcher(t *testing.T) {
	re, err := TestMatcher(config.DefaultTestPattern)
	if err != nil {
		t.Fatal(err)
	}
	for command, want := range map[string]bool{
		"make check":                            true,
		"TMPDIR=/tmp make check 2>&1 | tail -5": true,
		"cd x && go test ./...":                 true,
		"gofmt -l .; make test":                 true,
		`grep -rn "go test" .github/`:           false,
		`git commit -m "build: Run make check"`: false,
		"make build":                            false,
	} {
		if got := re.MatchString(command); got != want {
			t.Errorf("%q matched %v, want %v", command, got, want)
		}
	}
	if re, err := TestMatcher(" "); re != nil || err != nil {
		t.Errorf("an empty pattern = %v, %v", re, err)
	}
	if _, err := TestMatcher("("); err == nil {
		t.Error("a broken pattern compiled")
	}
}

func TestPRPrefersTheOneOpened(t *testing.T) {
	s := Signals{PRs: []string{"https://github.com/o/r/pull/20", "https://github.com/o/r/pull/12"},
		Opened: []string{"https://github.com/o/r/pull/20"}}
	if got := s.PR("https://github.com/o/r"); got != "https://github.com/o/r/pull/20" {
		t.Errorf("PR = %q", got)
	}
}

func TestStateOfAsksOnceForSeveralPolls(t *testing.T) {
	saved := forge
	defer func() { forge = saved }()
	var mu sync.Mutex
	calls := 0
	release := make(chan struct{})
	forge = func(name string, args ...string) ([]byte, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		<-release
		return []byte(`{"state": "OPEN", "headRefName": "x"}`), nil
	}
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := StateOf("https://github.com/o/r/pull/77"); got.State != "open" {
				t.Errorf("state = %+v", got)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if calls != 1 {
		t.Errorf("the forge was asked %d times", calls)
	}
}
