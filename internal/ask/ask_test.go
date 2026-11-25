package ask

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rouxlabs/headcoach/internal/store"
	"github.com/rouxlabs/headcoach/internal/transcript"
)

func TestPromptHoldsTheRecentTurnsAndADefaultQuestion(t *testing.T) {
	task := &store.Task{Title: "Fix it", State: "review", Last: "Done"}
	var turns []transcript.Turn
	for i := 0; i < 60; i++ {
		turns = append(turns, transcript.Turn{Who: "you", Text: fmt.Sprintf("turn %d", i)})
	}
	turns = append(turns, transcript.Turn{Who: "agent", Text: strings.Repeat("x", 5000)})
	prompt := BuildPrompt(task, turns, "  ")
	if !strings.Contains(prompt, "USER: turn 59") || strings.Contains(prompt, "USER: turn 20\n") {
		t.Error("the prompt does not hold exactly the last 40 turns")
	}
	if !strings.Contains(prompt, "AGENT: "+strings.Repeat("x", 1500)+" [...]") {
		t.Error("a long turn is not cut")
	}
	if !strings.Contains(prompt, "Latest activity: Done\n") || !strings.HasSuffix(prompt, "Question: "+DefaultQuestion) {
		t.Errorf("prompt ends with %q", prompt[len(prompt)-80:])
	}
	if got := BuildPrompt(task, turns, "what is left?"); !strings.HasSuffix(got, "Question: what is left?") {
		t.Error("the question is not at the end")
	}
	// The prompt the agent works on is kept, however long the turn.
	long := []transcript.Turn{{Who: "you", Text: "fix the build"}}
	for i := 0; i < 50; i++ {
		long = append(long, transcript.Turn{Who: "tool", Text: fmt.Sprintf("step %d", i)})
	}
	if got := BuildPrompt(task, long, ""); !strings.Contains(got, "USER: fix the build\n\n… 10 turns …\n\nTOOL: step 10\n") {
		t.Errorf("a long turn lost its prompt:\n%.300s", got)
	}
}

func TestCommandKeepsTheLauncherAndNamesTheModel(t *testing.T) {
	for _, c := range []struct{ launcher, model, want string }{
		{"claude-team", "haiku", "claude-team -p --no-session-persistence --model haiku"},
		{"CLAUDE_CONFIG_DIR=~/.c claude", "", "CLAUDE_CONFIG_DIR=~/.c claude -p --no-session-persistence"},
		{"claude", "a b;c", "claude -p --no-session-persistence --model 'a b;c'"},
	} {
		if got := Command(c.launcher, c.model); got != c.want {
			t.Errorf("Command = %q, want %q", got, c.want)
		}
	}
}

// fakeClaude writes a script that stands in for Claude Code, and returns its path.
func fakeClaude(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRun(t *testing.T) {
	t.Setenv("HC_TASK", "outer")
	tmp := t.TempDir()
	// The helper run is marked so the hook ignores it, and does not inherit the task of the caller.
	claude := fakeClaude(t, `echo " $* | $(cat) | ${HC_IGNORE} | ${HC_TASK:-unset} | $(pwd) "`)
	got, err := Run(claude, "haiku", "the prompt", tmp)
	real, _ := filepath.EvalSymlinks(tmp)
	if want := "-p --no-session-persistence --model haiku | the prompt | 1 | unset | " + real; err != nil || got != want {
		t.Errorf("Run = %q, %v\nwant  %q", got, err, want)
	}

	_, err = Run(fakeClaude(t, "echo boom >&2; echo 'bad model' >&2; exit 1"), "", "p", tmp)
	if err == nil || err.Error() != "Claude Code could not answer: bad model" {
		t.Errorf("a failing run gave %v", err)
	}
	_, err = Run(fakeClaude(t, "exit 0"), "", "p", tmp)
	if err == nil || err.Error() != "Claude Code could not answer: no output" {
		t.Errorf("a silent run gave %v", err)
	}
	_, err = Run("/no/such/claude", "", "p", tmp)
	if err == nil || !strings.HasPrefix(err.Error(), "Claude Code could not answer: ") {
		t.Errorf("a missing launcher gave %v", err)
	}

	Timeout = 300 * time.Millisecond
	defer func() { Timeout = 120 * time.Second }()
	start := time.Now()
	_, err = Run(fakeClaude(t, "sleep 30"), "", "p", tmp)
	if err == nil || err.Error() != "No answer after 0 seconds" || time.Since(start) > 5*time.Second {
		t.Errorf("a slow run gave %v after %v", err, time.Since(start))
	}
}
