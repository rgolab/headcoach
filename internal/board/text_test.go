package board

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/rouxlabs/headcoach/internal/store"
)

func TestClip(t *testing.T) {
	for _, c := range []struct {
		in   string
		n    int
		want string
	}{
		{"abc", 3, "abc"},
		{"  a \n\t b   c ", 50, "a b c"},
		{"abcdefghij", 5, "abcd…"},
		{"ab cdef", 4, "ab…"},
		{"héllo wörld", 6, "héllo…"},
	} {
		if got := Clip(c.in, c.n); got != c.want {
			t.Errorf("Clip(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
	for _, n := range []int{1, 2, 10, 100, 300} {
		if got := utf8.RuneCountInString(Clip(strings.Repeat("word ", 200), n)); got > n {
			t.Errorf("Clip to %d gave %d characters", n, got)
		}
	}
}

func TestFirstLineAndTitle(t *testing.T) {
	if got := FirstLine("\n   \r\n  hello  \nworld"); got != "hello" {
		t.Errorf("FirstLine = %q", got)
	}
	if got := TitleFromPrompt("\n\n   fix the bug  \nand more detail"); got != "fix the bug" {
		t.Errorf("TitleFromPrompt = %q", got)
	}
	long := TitleFromPrompt(strings.Repeat("x", 5000))
	if n := utf8.RuneCountInString(long); n < 20 || n > 100 || !strings.HasSuffix(long, "…") {
		t.Errorf("a long title is %d characters: %q", n, long)
	}
	for _, prompt := range []string{"", "   ", "\n\n", "\t\n  \n"} {
		if got := TitleFromPrompt(prompt); got != "Untitled session" {
			t.Errorf("TitleFromPrompt(%q) = %q", prompt, got)
		}
	}
}

func TestClosingQuestion(t *testing.T) {
	for in, want := range map[string]string{
		"Should I continue?":                         "Should I continue?",
		"Done.\n\nShall I continue?   \n\n  \n":      "Shall I continue?",
		"I changed.\n\nIs that fine?\n\nAlso, docs?": "Also, docs?",
		"Is it ok?\nYes it is.":                      "",
		"":                                           "",
		"   \n ":                                     "",
		"All done.":                                  "",
		"Fixed it!":                                  "",
		"What? No.":                                  "",
		"- Should I proceed?":                        "Should I proceed?",
		"> ## Should I proceed?":                     "Should I proceed?",
		"* Should I proceed?":                        "Should I proceed?",
		"**Should I proceed?**":                      "Should I proceed?",
		"_Should I proceed?_":                        "Should I proceed?",
		"`Should I proceed?`":                        "Should I proceed?",
		"Reply \"go\" and I push it.":                "",
	} {
		if got := ClosingQuestion(in); got != want {
			t.Errorf("ClosingQuestion(%q) = %q, want %q", in, got, want)
		}
	}
	if got := ClosingQuestion(strings.Repeat("why ", 500) + "?"); utf8.RuneCountInString(got) > 300 || got == "" {
		t.Errorf("a long question is not clipped: %d characters", utf8.RuneCountInString(got))
	}
}

func TestClosingAsk(t *testing.T) {
	for in, want := range map[string]string{
		"Plan below.\n\nReply \"go\", or change the colour.":       "Reply \"go\", or change the colour.",
		"**Say go and I push it.**":                                "Say go and I push it.",
		"- Pick one: A or B.":                                      "Pick one: A or B.",
		"Confirm and I tag v1.2.0.":                                "Confirm and I tag v1.2.0.",
		"Ready to tag v1.2.0. Reply \"go\" to tag.":                "Ready to tag v1.2.0. Reply \"go\" to tag.",
		"1. Reply \"go\" to start.":                                "1. Reply \"go\" to start.",
		"**Next:** reply \"go\".":                                  "Next:** reply \"go\".",
		"Answer yes and I merge it.":                               "Answer yes and I merge it.",
		"The plan waits for your go.":                              "The plan waits for your go.",
		"That is your call.":                                       "That is your call.",
		"Stopped here, waiting on you.":                            "Stopped here, waiting on you.",
		"Reply sent.\nAll done.":                                   "",
		"Reply sent to the thread.":                                "",
		"Answer: the cache key was wrong.":                         "",
		"Confirm dialog now closes on Esc.":                        "",
		"Pick-up times are fixed.":                                 "",
		"Approve button is wired up.":                              "",
		"Updated your Go toolchain to 1.23.":                       "",
		"Fixed your call site in main.go.":                         "",
		"Your OK button now submits the form.":                     "",
		"Nothing is waiting on you.":                               "",
		"Let me know if you want more.":                            "",
		"I picked the faster one.":                                 "",
		"Saying go was not needed.":                                "",
		"Should I go?":                                             "",
		"Push it? Reply \"go\" to tag.":                            "Push it? Reply \"go\" to tag.",
		"Push it? Reply go.":                                       "Push it? Reply go.",
		"If the plan looks right, reply \"go\".":                   "If the plan looks right, reply \"go\".",
		"Just reply go and I tag it.":                              "Just reply go and I tag it.",
		"Reply with \"go\" to start.":                              "Reply with \"go\" to start.",
		"Not urgent, the tag waits for your go.":                   "Not urgent, the tag waits for your go.",
		"Answer: no, nothing reads that cache.":                    "",
		"Answer: yes, it is thread-safe.":                          "",
		"Added the prompt. Type `yes` to confirm, `no` to cancel.": "",
		"Try it: type `go` in the prompt bar.":                     "",
		"The script asks first; say no and it exits.":              "",
		"It now builds with your Go.":                              "",
		"The hook now logs your call.":                             "",
		"No one is waiting on you.":                                "",
		"":                                                         "",
	} {
		if got := ClosingAsk(in); got != want {
			t.Errorf("ClosingAsk(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSummarizeTool(t *testing.T) {
	m := func(pairs ...string) map[string]any {
		out := map[string]any{}
		for i := 0; i < len(pairs); i += 2 {
			out[pairs[i]] = pairs[i+1]
		}
		return out
	}
	for _, c := range []struct {
		tool  string
		input any
		want  string
	}{
		{"Bash", m("command", "ls -la\necho hi"), "$ ls -la"},
		{"Bash", m(), "$"},
		{"Edit", m("file_path", "/a/b/c.py"), "Edit c.py"},
		{"Read", m("file_path", "/a/b/readme.md"), "Read readme.md"},
		{"Write", m(), "Write"},
		{"NotebookEdit", m("notebook_path", "/n/x.ipynb"), "NotebookEdit x.ipynb"},
		{"Grep", m("pattern", "foo.*bar"), "Grep foo.*bar"},
		{"Glob", m("pattern", "**/*.py"), "Glob **/*.py"},
		{"WebFetch", m("url", "https://example.org/x"), "WebFetch https://example.org/x"},
		{"WebSearch", m("query", "sqlite like escape"), "WebSearch sqlite like escape"},
		{"Skill", m("skill", "commit"), "Skill commit"},
		{"Agent", m("subagent_type", "Explore", "description", "find it"), "Agent Explore: find it"},
		{"Task", m(), "Agent"},
		{"mcp__server__tool", m(), "server: tool"},
		{"mcp__server__a__b", m(), "server: b"},
		{"mcp__x", m(), "mcp__x"},
		{"SomethingNew", map[string]any{"a": 1.0}, "SomethingNew"},
		{"", m(), ""},
		{"Bash", nil, "$"},
		{"Bash", "a string", "$"},
		{"Edit", 42.0, "Edit"},
		{"Other", []any{"x"}, "Other"},
	} {
		if got := SummarizeTool(c.tool, c.input); got != c.want {
			t.Errorf("SummarizeTool(%q, %v) = %q, want %q", c.tool, c.input, got, c.want)
		}
	}
	if got := SummarizeTool("Bash", m("command", strings.Repeat("x", 1000))); utf8.RuneCountInString(got) > 100 {
		t.Error("a long command is not clipped")
	}
}

func TestWindowName(t *testing.T) {
	const id = "7d4f82cd-1111-4222-8333-444455556666"
	for _, c := range []struct{ title, prompt, want string }{
		{"review PROJ-1234 (MR !12)", "", "7d4f82cd-PROJ-1234"},
		{"Fix the login redirect", "See PROJ-12 for details", "7d4f82cd-PROJ-12"},
		{"Fix flaky test_parser_timeout on CI", "", "7d4f82cd-fix-flaky-test"},
		{"Internationalisation of extraordinarily long names", "", "7d4f82cd-internationalisation-of"},
		{"???", "", "7d4f82cd"},
	} {
		if got := WindowName(&store.Task{ID: id, Title: c.title, Prompt: c.prompt}); got != c.want {
			t.Errorf("WindowName(%q) = %q, want %q", c.title, got, c.want)
		}
	}
}

func TestMRNumber(t *testing.T) {
	for title, want := range map[string]string{
		"review PROJ-1234 (MR !12)": "12",
		"!7 is ready":               "7",
		"see group/project!12":      "",
		"wow!12":                    "",
		"no reference":              "",
	} {
		if got := MRNumber(&store.Task{Title: title}); got != want {
			t.Errorf("MRNumber(%q) = %q, want %q", title, got, want)
		}
	}
}

func TestRemoteWebURL(t *testing.T) {
	for remote, want := range map[string]string{
		"git@github.com:acme/tool.git":            "https://github.com/acme/tool",
		"ssh://git@git.example.org:2222/a/b.git":  "https://git.example.org/a/b",
		"https://github.com/acme/tool.git\n":      "https://github.com/acme/tool",
		"https://gitlab.example.org/group/sub/p/": "https://gitlab.example.org/group/sub/p",
		"https://user:secret@example.org/a/b":     "",
		"/srv/git/local.git":                      "",
		"":                                        "",
	} {
		if got := RemoteWebURL(remote); got != want {
			t.Errorf("RemoteWebURL(%q) = %q, want %q", remote, got, want)
		}
	}
}

func gitInit(t *testing.T, dir, branch string) {
	t.Helper()
	if out, err := exec.Command("git", "init", "-q", "-b", branch, dir).CombinedOutput(); err != nil {
		t.Skipf("git is not usable here: %v %s", err, out)
	}
}

func TestGitInfo(t *testing.T) {
	tmp := t.TempDir()
	if repo, branch := GitInfo(""); repo != "" || branch != "" {
		t.Error("an empty directory has git information")
	}
	if repo, _ := GitInfo(filepath.Join(tmp, "nope")); repo != "" {
		t.Error("a missing directory has a repo name")
	}
	plain := filepath.Join(tmp, "plain")
	os.Mkdir(plain, 0o755)
	if repo, branch := GitInfo(plain + "/"); repo != "plain" || branch != "" {
		t.Errorf("plain directory = %q %q", repo, branch)
	}
	repo := filepath.Join(tmp, "myrepo")
	os.MkdirAll(filepath.Join(repo, "sub"), 0o755)
	gitInit(t, repo, "trunk")
	if name, branch := GitInfo(filepath.Join(repo, "sub")); name != "myrepo" || branch != "trunk" {
		t.Errorf("repo = %q %q", name, branch)
	}
}

func TestNewID(t *testing.T) {
	a, b := NewID(), NewID()
	if a == b || !sessionIDRe.MatchString(a) || len(a) != 36 || a[14] != '4' {
		t.Errorf("NewID gave %q then %q", a, b)
	}
}
