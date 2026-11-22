package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// choice is one column of the board, in the order you set, and whether the board shows it.
type choice struct {
	col   int // its index in columns
	shown bool
}

const columnsHelp = `j / k, arrows   move the cursor
g / G           go to the top or the bottom
space           show or hide the column under the cursor
J / K           move the column down or up
r               back to the default columns
esc or q        back to the board

TASK stays first and TITLE stays shown: the keys act on the task, and sub-agents are drawn
in its title. Each change is saved at once, and the board keeps it after a restart.`

// columnsHintKeys are the keys shown in the header of the columns view.
var columnsHintKeys = [][]hint{
	{{"space", "show/hide"}, {"J/K", "move"}, {"r", "default"}},
	{{"esc", "back"}, {"?", "help"}},
}

// pinned says what keeps a column in place: TASK stays first, TITLE stays shown. Empty: nothing.
func pinned(col int) string {
	switch columns[col].name {
	case "TASK":
		return "always first"
	case "TITLE":
		return "always shown"
	}
	return ""
}

// defaultLayout is every column, shown, in the order of columns.
func defaultLayout() []choice {
	out := make([]choice, len(columns))
	for i := range columns {
		out[i] = choice{i, true}
	}
	return out
}

// readLayout reads the columns saved in path: one name per line, in order, a hidden one after a
// "-". An unknown name is skipped, a column missing from the file, one a later release added, is
// shown after the column before it in the default order, and TASK and TITLE are put back in place
// whatever the file says. No file, or no path: the default.
func readLayout(path string) []choice {
	data, err := os.ReadFile(path)
	if path == "" || err != nil {
		return defaultLayout()
	}
	seen := map[int]bool{}
	var out []choice
	for _, line := range strings.Split(string(data), "\n") {
		name, hidden := strings.CutPrefix(strings.TrimSpace(line), "-")
		name = strings.ToUpper(strings.TrimSpace(name))
		i := slices.IndexFunc(columns, func(col column) bool { return col.name == name })
		if i < 0 || seen[i] {
			continue
		}
		seen[i] = true
		out = append(out, choice{i, !hidden})
	}
	for i := range columns {
		if seen[i] {
			continue
		}
		// Right after the column before it in the default order: the loop put that one in already.
		at := slices.IndexFunc(out, func(ch choice) bool { return ch.col == i-1 }) + 1
		out = slices.Insert(out, at, choice{i, true})
		seen[i] = true
	}
	for i := range out {
		if pinned(out[i].col) != "" {
			out[i].shown = true
		}
	}
	if i := slices.IndexFunc(out, func(ch choice) bool { return pinned(ch.col) == "always first" }); i > 0 {
		first := out[i]
		out = append([]choice{first}, slices.Delete(out, i, i+1)...)
	}
	return out
}

// writeLayout saves the columns to path, in the form readLayout reads.
func writeLayout(path string, layout []choice) error {
	var out strings.Builder
	for _, ch := range layout {
		if !ch.shown {
			out.WriteString("-")
		}
		out.WriteString(columns[ch.col].name + "\n")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(out.String()), 0o644)
}

// shownColumns and shownCells keep the columns of the board, and the cells of one row, that the
// layout shows, in its order.
func (m model) shownColumns() []column {
	var out []column
	for _, ch := range m.layout {
		if ch.shown {
			out = append(out, columns[ch.col])
		}
	}
	return out
}

// shownWidths keeps the widths of the shown columns, from every row. Nil before the first rebuild:
// the table then measures the rows on screen.
func (m model) shownWidths() []int {
	if len(m.widths) != len(columns) {
		return nil
	}
	var out []int
	for _, ch := range m.layout {
		if ch.shown {
			out = append(out, m.widths[ch.col])
		}
	}
	return out
}

func (m model) shownCells(cells []cell) []cell {
	var out []cell
	for _, ch := range m.layout {
		if ch.shown {
			out = append(out, cells[ch.col])
		}
	}
	return out
}

// setLayout takes a new layout and saves it. A save that fails says so, the board keeps the
// layout until it closes.
func (m *model) setLayout(layout []choice, notice string) {
	m.layout = layout
	if m.layoutPath != "" {
		if err := writeLayout(m.layoutPath, layout); err != nil {
			m.say("Could not save the columns: "+err.Error(), true)
			return
		}
	}
	if notice != "" {
		m.say(notice, false)
	}
}

// columnsKey handles a key on the list of columns.
func (m model) columnsKey(key string) (tea.Model, tea.Cmd) {
	last := len(m.layout) - 1
	switch key {
	case "Q":
		return m, tea.Quit
	case "esc", "q", "C":
		m.view = viewBoard
	case "j", "down":
		m.colCursor = min(m.colCursor+1, last)
	case "k", "up":
		m.colCursor = max(m.colCursor-1, 0)
	case "g", "home":
		m.colCursor = 0
	case "G", "end":
		m.colCursor = last
	case "?":
		m.note("Column keys (any key closes)", columnsHelp, nil)
	case "r":
		m.colCursor = 0
		m.setLayout(defaultLayout(), "Default columns")
	case " ":
		ch := m.layout[m.colCursor]
		if pinned(ch.col) != "" {
			m.say(columns[ch.col].name+" is always shown", true)
			return m, nil
		}
		layout := slices.Clone(m.layout)
		layout[m.colCursor].shown = !ch.shown
		m.setLayout(layout, "")
	case "J", "shift+down", "K", "shift+up":
		to := m.colCursor + 1
		if key == "K" || key == "shift+up" {
			to = m.colCursor - 1
		}
		if to < 0 || to > last {
			return m, nil
		}
		if pinned(m.layout[m.colCursor].col) == "always first" || pinned(m.layout[to].col) == "always first" {
			m.say("TASK stays first", true)
			return m, nil
		}
		layout := slices.Clone(m.layout)
		layout[m.colCursor], layout[to] = layout[to], layout[m.colCursor]
		m.colCursor = to
		m.setLayout(layout, "")
	}
	return m, nil
}

// columnsView is the list of columns: a box each, in the order of the board.
func (m model) columnsView() string {
	height := m.tableHeight()
	top := max(0, m.colCursor-height+1)
	shown := 0
	var rows [][]cell
	for i, ch := range m.layout {
		if ch.shown {
			shown++
		}
		if i < top || i >= top+height {
			continue
		}
		box := c("[ ]", dim)
		if ch.shown {
			box = c("[x]", accent)
		}
		name := c(columns[ch.col].name, plain)
		if !ch.shown {
			name.style = dim
		}
		rows = append(rows, []cell{box, name, c(pinned(ch.col), dim)})
	}
	table := tableLines([]column{{"", false}, {"COLUMN", false}, {"", false}}, nil, rows, m.colCursor-top, max(10, m.width-2))
	title := []cell{c("columns", stateStyles["parked"].Bold(true)), c(fmt.Sprintf("[%d of %d shown]", shown, len(m.layout)), plain)}
	return m.frame(title, table, []cell{c("space shows or hides a column, J and K move it. Each change is saved at once.", dim)})
}
