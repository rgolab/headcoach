package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rouxlabs/headcoach/internal/api"
	"github.com/rouxlabs/headcoach/internal/client"
)

// reloaded is the board a reload opens: a fresh one given the saved view, then the same polls.
func reloaded(t *testing.T, saved string, msgs ...tea.Msg) model {
	t.Helper()
	m := newModel(client.New("http://127.0.0.1:1"))
	m.restoreState(saved)
	return send(t, m, append([]tea.Msg{tea.WindowSizeMsg{Width: 200, Height: 20}}, msgs...)...)
}

func TestReloadKeepsTheBoard(t *testing.T) {
	var tasks []api.Task
	for i := range 30 {
		state := "running"
		if i%3 == 0 {
			state = "done"
		}
		tasks = append(tasks, task(fmt.Sprintf("aaaa%04d", i), state, i))
	}
	poll := boardMsg{board: api.Tasks{Tasks: tasks}}
	m := loaded(t, nil, tasks...)
	m, _ = press(t, m, "d", "o", "G", "k", "k")
	if m.top == 0 || !m.showDone {
		t.Fatalf("the board did not scroll: top %d, show done %v", m.top, m.showDone)
	}
	saved := m.saveState()

	got := reloaded(t, saved, poll)
	if !got.showDone || got.order != m.order || got.view != viewBoard {
		t.Errorf("show done %v, order %q, view %v: want true, %q and the board", got.showDone, got.order, got.view, m.order)
	}
	if got.keys[got.cursor] != m.keys[m.cursor] || got.top != m.top {
		t.Errorf("cursor on %s, top %d: want %s, %d", got.keys[got.cursor], got.top, m.keys[m.cursor], m.top)
	}
	if got.View() != m.View() {
		t.Errorf("the board looks different after a reload:\n%s\nwant:\n%s", got.View(), m.View())
	}

	// The filter, and the detail of a task scrolled down.
	m.needle = "aaaa001"
	m.rebuild()
	m, _ = press(t, m, "enter")
	long := task(m.detailID, "running", 1)
	long.Title = strings.Repeat("line\n", 200) // longer than the screen
	m = send(t, m, detailMsg{task: long})
	m, _ = press(t, m, "j", "j", "j")
	if m.view != viewDetail || m.detail.YOffset == 0 {
		t.Fatalf("view %v, detail offset %d: the detail did not open and scroll", m.view, m.detail.YOffset)
	}
	got = reloaded(t, m.saveState(), poll, detailMsg{task: long})
	if got.view != viewDetail || got.detailID != m.detailID || got.needle != "aaaa001" {
		t.Errorf("view %v, detail %q, filter %q", got.view, got.detailID, got.needle)
	}
	if got.detail.YOffset != m.detail.YOffset {
		t.Errorf("detail offset %d, want %d", got.detail.YOffset, m.detail.YOffset)
	}
	if got.Init() == nil {
		t.Error("no command at start")
	}

	// A value the board cannot read opens a fresh board.
	for _, bad := range []string{"", "{", `{"view":"note","order":"nope","column":-3}`} {
		got := reloaded(t, bad, poll)
		if got.view != viewBoard || got.showDone || got.order != "state" || got.colCursor != 0 {
			t.Errorf("%q: view %v, show done %v, order %q, column %d", bad, got.view, got.showDone, got.order, got.colCursor)
		}
	}
}

func TestReloadEnv(t *testing.T) {
	t.Setenv(stateEnv, `{"show_done":true}`)
	m := newModel(client.New("http://127.0.0.1:1"))
	var found []string
	for _, kv := range m.reloadEnv() {
		if strings.HasPrefix(kv, stateEnv+"=") {
			found = append(found, kv)
		}
	}
	if len(found) != 1 || found[0] != stateEnv+"="+m.saveState() {
		t.Errorf("the reload hands over %v, want the board's view once", found)
	}
}

func TestReloadWithoutItsRow(t *testing.T) {
	var tasks []api.Task
	for i := range 30 {
		tasks = append(tasks, task(fmt.Sprintf("aaaa%04d", i), "running", i))
	}
	m := loaded(t, nil, tasks...)
	m, _ = press(t, m, "G", "k", "k")
	saved, row, top := m.saveState(), m.cursor, m.top

	// The task under the cursor is gone by the first poll: the cursor keeps its place.
	gone := slices.DeleteFunc(slices.Clone(tasks), func(t api.Task) bool { return t.ID == m.keys[row] })
	got := reloaded(t, saved, boardMsg{board: api.Tasks{Tasks: gone}})
	// One row fewer: the table scrolls as far as it can.
	if want := min(top, len(got.rows)-got.tableHeight()); got.cursor != row || got.top != want {
		t.Errorf("cursor %d, top %d: want %d, %d", got.cursor, got.top, row, want)
	}

	// The detail it was scrolled in is left: another task's detail opens at its top.
	got = reloaded(t, `{"view":"detail","detail":"aaaa0001","detail_top":40}`, boardMsg{board: api.Tasks{Tasks: tasks}})
	got, _ = press(t, got, "esc", "enter")
	long := task(got.detailID, "running", 1)
	long.Title = strings.Repeat("line\n", 200)
	if got = send(t, got, detailMsg{task: long}); got.detail.YOffset != 0 {
		t.Errorf("%s opens at line %d of its detail", got.detailID, got.detail.YOffset)
	}
}
