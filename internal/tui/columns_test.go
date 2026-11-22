package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// names lists the columns of a layout, a hidden one after a "-".
func names(layout []choice) string {
	var out []string
	for _, ch := range layout {
		name := columns[ch.col].name
		if !ch.shown {
			name = "-" + name
		}
		out = append(out, name)
	}
	return strings.Join(out, " ")
}

const allColumns = "TASK STATE PROFILE MODEL EFFORT PERSONA REPO TMUX AGE IDLE CTX IN OUT CACHE COST TIMER SCHEDULE TITLE LAST"

func TestTheColumnsViewHidesMovesAndSaves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "columns")
	m := loaded(t, nil, task("aaaa0001", "running", 1))
	m.layout, m.layoutPath = readLayout(path), path
	if names(m.layout) != allColumns {
		t.Fatalf("without a file the layout is %q", names(m.layout))
	}
	m, _ = press(t, m, "C")
	if m.view != viewColumns || !strings.Contains(m.View(), "[19 of 19 shown]") || !strings.Contains(m.View(), "always first") {
		t.Fatalf("C did not open the columns:\n%s", m.View())
	}

	// PROFILE hidden, then MODEL moved up above STATE.
	m, _ = press(t, m, "j", "j", " ", "j", "K", "K")
	if got := names(m.layout); !strings.HasPrefix(got, "TASK MODEL STATE -PROFILE EFFORT") || m.colCursor != 1 {
		t.Errorf("layout %q, cursor %d", got, m.colCursor)
	}
	// Nothing moves above TASK, and TASK and TITLE stay shown.
	m, _ = press(t, m, "K")
	if !strings.Contains(m.notice, "TASK stays first") || !strings.HasPrefix(names(m.layout), "TASK MODEL") {
		t.Errorf("K above TASK: %q, %q", m.notice, names(m.layout))
	}
	m, _ = press(t, m, "g", " ")
	if !strings.Contains(m.notice, "TASK is always shown") || !m.layout[0].shown {
		t.Errorf("space on TASK: %q", m.notice)
	}
	if data, err := os.ReadFile(path); err != nil || !strings.HasPrefix(string(data), "TASK\nMODEL\nSTATE\n-PROFILE\nEFFORT\n") {
		t.Errorf("saved %q, %v", data, err)
	}

	// The board draws the layout: no PROFILE, MODEL before STATE.
	m, _ = press(t, m, "esc")
	head := strings.Fields(strings.Trim(strings.Split(m.View(), "\n")[headerHeight+1], "│ "))
	if m.view != viewBoard || strings.Contains(strings.Join(head, " "), "PROFILE") || head[0] != "TASK" || head[1] != "MODEL" {
		t.Errorf("board heading %v", head)
	}

	// A restart reads it back; r goes back to the default.
	fresh := loaded(t, nil)
	fresh.layout, fresh.layoutPath = readLayout(path), path
	if names(fresh.layout) != names(m.layout) {
		t.Errorf("read back %q, want %q", names(fresh.layout), names(m.layout))
	}
	fresh, _ = press(t, fresh, "C", "r")
	if names(fresh.layout) != allColumns || names(readLayout(path)) != allColumns {
		t.Errorf("r left %q", names(fresh.layout))
	}
}

func TestReadLayoutMendsTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "columns")
	if err := os.WriteFile(path, []byte("cost\n-title\nNOPE\n\nTASK\n-state\ncost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// TASK goes first, TITLE is shown, NOPE and the second COST are dropped, the missing ones come in
	// right after the column before them in the default order.
	want := "TASK COST TIMER SCHEDULE TITLE LAST -STATE PROFILE MODEL EFFORT PERSONA REPO TMUX AGE IDLE CTX IN OUT CACHE"
	if got := names(readLayout(path)); got != want {
		t.Errorf("layout %q, want %q", got, want)
	}
	// A file saved before MODEL, EFFORT, TIMER and SCHEDULE existed, with PROFILE hidden and LAST moved up.
	old := "TASK\nLAST\nSTATE\n-PROFILE\nPERSONA\nREPO\nTMUX\nAGE\nIDLE\nCTX\nIN\nOUT\nCACHE\nCOST\nTITLE\n"
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	want = "TASK LAST STATE -PROFILE MODEL EFFORT PERSONA REPO TMUX AGE IDLE CTX IN OUT CACHE COST TIMER SCHEDULE TITLE"
	if got := names(readLayout(path)); got != want {
		t.Errorf("an older file gave %q, want %q", got, want)
	}
	if got := names(readLayout(filepath.Join(t.TempDir(), "missing"))); got != allColumns {
		t.Errorf("a missing file gave %q", got)
	}
}

func TestASaveThatFailsSaysSo(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	m := loaded(t, nil, task("aaaa0001", "running", 1))
	m.layoutPath = filepath.Join(blocker, "columns") // under a file: no folder can be made
	m, _ = press(t, m, "C", "j", " ")
	if !m.noticeAlarm || !strings.Contains(m.notice, "Could not save the columns") || m.layout[1].shown {
		t.Errorf("notice %q, layout %q", m.notice, names(m.layout))
	}
}

func TestColumnsCommand(t *testing.T) {
	m := loaded(t, nil, task("aaaa0001", "running", 1))
	got, _ := line(t, m, "columns")
	if got.view != viewColumns {
		t.Errorf(":columns opened view %v", got.view)
	}
	if !got.idle() { // each change is saved at once: an update may reload the board here
		t.Error("the columns view holds back a reload")
	}
}
