package tui

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/humblebundledore/headcoach/internal/update"
)

// fakeRelease stubs what :update and :restart reach outside the board.
func fakeRelease(t *testing.T, rel update.Release, newer bool, err error) (installed, restarted *int) {
	t.Helper()
	installed, restarted = new(int), new(int)
	check, inst, restart := checkRelease, installRelease, restartDaemon
	t.Cleanup(func() { checkRelease, installRelease, restartDaemon = check, inst, restart })
	checkRelease = func(string) (update.Release, bool, error) { return rel, newer, err }
	installRelease = func(string, string) (string, error) { *installed++; return "/bin/hc", nil }
	restartDaemon = func() error { *restarted++; return nil }
	return installed, restarted
}

// line types a command on the : line and runs what it starts, one level deep.
func line(t *testing.T, m model, text string) (model, tea.Cmd) {
	t.Helper()
	m, _ = press(t, m, ":")
	m.input.SetValue(text)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return next.(model), cmd
}

func TestCommandLine(t *testing.T) {
	m := loaded(t, nil, task("aaaa0001", "running", 1))
	if m, _ = press(t, m, ":"); m.asking != "command" {
		t.Fatalf(": opened %q", m.asking)
	}

	m = loaded(t, nil, task("aaaa0001", "running", 1))
	if _, cmd := line(t, m, "q"); cmd == nil || fmt.Sprint(cmd()) != fmt.Sprint(tea.Quit()) {
		t.Error(":q does not quit")
	}
	if got, _ := line(t, m, "nope"); !strings.Contains(got.notice, "Unknown command nope. Known: update, restart, columns, help, q") {
		t.Errorf("unknown: %q", got.notice)
	}
	if got, _ := line(t, m, "help"); got.view != viewNote || !strings.Contains(got.noteText, "install the latest release") {
		t.Errorf("help: view %v %q", got.view, got.noteText)
	}

	_, restarted := fakeRelease(t, update.Release{}, false, nil)
	_, cmd := line(t, m, "restart")
	if done := cmd().(doneMsg); done.notice != "Daemon restarted" || *restarted != 1 {
		t.Errorf("restart: %+v, %d", done, *restarted)
	}
}

func TestCommandUpdate(t *testing.T) {
	m := loaded(t, nil, task("aaaa0001", "running", 1))

	// A newer release: its notes, then y installs it and restarts the daemon.
	installed, restarted := fakeRelease(t, update.Release{Tag: "v9.0.0", Notes: "- shiny"}, true, nil)
	m, cmd := line(t, m, "update")
	m = send(t, m, cmd())
	if m.view != viewNote || !strings.Contains(m.noteText, "- shiny") || !strings.Contains(m.noteTitle, "v9.0.0") {
		t.Fatalf("view %v %q %q", m.view, m.noteTitle, m.noteText)
	}
	_, cmd = press(t, m, "y")
	if cmd == nil {
		t.Fatal("y started nothing")
	}
	// tea.Sequence hands its steps over in one message: run them in order, as the program would.
	steps := fmt.Sprintf("%#v", cmd())
	if !strings.Contains(steps, "sequenceMsg") {
		t.Fatalf("y did not start a sequence: %s", steps)
	}
	seq := reflect.ValueOf(cmd())
	first := send(t, m, seq.Index(0).Interface().(tea.Cmd)())
	if !strings.Contains(first.notice, "Installing v9.0.0") || *installed != 0 {
		t.Errorf("before the download: %q, installed %d", first.notice, *installed)
	}
	done := send(t, first, seq.Index(1).Interface().(tea.Cmd)())
	if *installed != 1 || *restarted != 1 || !strings.Contains(done.notice, "v9.0.0 installed") {
		t.Errorf("install: %d %d %q", *installed, *restarted, done.notice)
	}

	// A release without notes asks straight away.
	fakeRelease(t, update.Release{Tag: "v9.0.0"}, true, nil)
	bare, cmd := line(t, loaded(t, nil), "update")
	if bare = send(t, bare, cmd()); !strings.HasPrefix(bare.noteText, "Install v9.0.0") {
		t.Errorf("empty notes: %q", bare.noteText)
	}

	// u on the board does the same as :update, and n installs nothing.
	installed, _ = fakeRelease(t, update.Release{Tag: "v9.0.0", Notes: "- shiny"}, true, nil)
	byKey, cmd := press(t, loaded(t, nil, task("aaaa0001", "running", 1)), "u")
	if cmd == nil {
		t.Fatal("u started nothing")
	}
	if byKey = send(t, byKey, cmd()); byKey.view != viewNote || !strings.Contains(byKey.noteTitle, "v9.0.0") {
		t.Fatalf("u: view %v %q", byKey.view, byKey.noteTitle)
	}
	if byKey, cmd = press(t, byKey, "n"); cmd != nil || *installed != 0 || byKey.view != viewBoard {
		t.Errorf("n after u: installed %d, view %v", *installed, byKey.view)
	}

	// u works on an empty board, and a second u waits for the first check.
	empty, check := press(t, loaded(t, nil), "u")
	if check == nil {
		t.Fatal("u on an empty board started nothing")
	}
	if empty, cmd = press(t, empty, "u"); cmd != nil || !strings.Contains(empty.notice, "Still looking") {
		t.Errorf("a second u: cmd %v, notice %q", cmd != nil, empty.notice)
	}
	if empty = send(t, empty, check()); empty.view != viewNote || empty.checking {
		t.Errorf("after the check: view %v, checking %v", empty.view, empty.checking)
	}

	// A check that returns while you type says so, instead of asking: your y or n is not its answer.
	typing, cmd := press(t, loaded(t, nil, task("aaaa0001", "running", 1)), "u")
	typing, _ = press(t, typing, "r")
	if typing = send(t, typing, cmd()); typing.view == viewNote || typing.asking != "reply" || !strings.Contains(typing.notice, "v9.0.0 is out") {
		t.Errorf("while typing: view %v, asking %q, notice %q", typing.view, typing.asking, typing.notice)
	}

	// Up to date, a build from source, GitHub unreachable.
	for _, c := range []struct {
		newer bool
		err   error
		want  string
	}{
		{false, nil, "is the latest release"},
		{false, update.ErrSource, "git pull && make install && hc restart"},
		{false, errors.New("404"), "Could not look for a release: 404"},
	} {
		fakeRelease(t, update.Release{}, c.newer, c.err)
		got, cmd := line(t, loaded(t, nil), "update")
		got = send(t, got, cmd())
		if !strings.Contains(got.notice+got.noteText, c.want) {
			t.Errorf("%v: notice %q, note %q", c.err, got.notice, got.noteText)
		}
	}
}
