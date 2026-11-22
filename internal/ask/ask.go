// Package ask answers a question about a session from the outside, without touching the session.
//
// The recent turns of its transcript go to a separate, short run of Claude Code in print mode. The session
// is not interrupted, its context does not grow, and nothing is added to its history.
package ask

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/humblebundledore/headcoach/internal/sh"
	"github.com/humblebundledore/headcoach/internal/store"
	"github.com/humblebundledore/headcoach/internal/transcript"
)

// DefaultQuestion is asked when the question is empty.
const DefaultQuestion = "Give me a short summary of where this session stands: what was asked, what is done, " +
	"and what is pending or blocked."

const (
	turns     = 40
	turnChars = 1500
)

// Timeout is how long Claude Code may take to answer.
var Timeout = 120 * time.Second

var who = map[string]string{"you": "USER", "agent": "AGENT", "tool": "TOOL"}

// BuildPrompt writes the prompt of the helper run: the recent turns, then the question.
func BuildPrompt(task *store.Task, log []transcript.Turn, question string) string {
	log = transcript.Window(log, turns)
	lines := make([]string, 0, len(log))
	for _, turn := range log {
		text := strings.TrimSpace(turn.Text)
		if runes := []rune(text); len(runes) > turnChars {
			text = strings.TrimRight(string(runes[:turnChars]), " \t\r\n") + " [...]"
		}
		speaker := who[turn.Who]
		if speaker == "" {
			speaker = "TOOL"
		}
		lines = append(lines, speaker+": "+text)
		if gap := turn.Gap(); gap != "" {
			lines = append(lines, gap)
		}
	}
	latest := task.Ask
	if latest == "" {
		latest = task.Last
	}
	if question = strings.TrimSpace(question); question == "" {
		question = DefaultQuestion
	}
	return "Below is the recent transcript of a Claude Code session, seen from the outside. " +
		"Answer the question about it. Be brief and concrete, plain text, no preamble. " +
		"Use only what the transcript shows, and say so when it does not show the answer. " +
		"Do not use tools.\n\n" +
		"Task: " + task.Title + "\nState: " + task.State + "\nLatest activity: " + latest + "\n\n" +
		"<transcript>\n" + strings.Join(lines, "\n\n") + "\n</transcript>\n\n" +
		"Question: " + question
}

// Command is the shell command of one print-mode run. The launcher may carry its own environment or flags.
func Command(launcher, model string) string {
	command := launcher + " -p --no-session-persistence"
	if model != "" {
		command += " --model " + sh.Quote(model)
	}
	return command
}

// Env is the environment of the helper run, from the daemon's own.
func Env(environ []string) []string {
	env := []string{"HC_IGNORE=1"} // the helper run must not become a task on the board
	for _, kv := range environ {
		if !strings.HasPrefix(kv, "HC_TASK=") && !strings.HasPrefix(kv, "HC_IGNORE=") {
			env = append(env, kv)
		}
	}
	return env
}

// Run asks Claude Code and returns its answer.
func Run(launcher, model, prompt, cwd string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", Command(launcher, model))
	cmd.Dir = cwd
	cmd.Env = Env(os.Environ())
	cmd.Stdin = strings.NewReader(prompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// The shell starts Claude Code as a child: on a timeout the whole group goes, not only the shell.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	if ctx.Err() != nil {
		return "", fmt.Errorf("No answer after %d seconds", int(Timeout.Seconds()))
	}
	answer := strings.TrimSpace(stdout.String())
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return "", fmt.Errorf("Could not run Claude Code: %v", err)
	}
	if err != nil || answer == "" {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = answer
		}
		if detail == "" {
			detail = "no output"
		}
		lines := strings.Split(detail, "\n")
		return "", fmt.Errorf("Claude Code could not answer: %s", lines[len(lines)-1])
	}
	return answer, nil
}
