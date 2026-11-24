package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/humblebundledore/headcoach/internal/api"
	"github.com/humblebundledore/headcoach/internal/format"
)

func TestSetTheme(t *testing.T) {
	defer applyTheme(themes[ThemeNames[0]])
	for _, name := range ThemeNames {
		if _, known := themes[name]; !known {
			t.Errorf("ThemeNames lists %q but themes does not define it", name)
		}
		if err := setTheme(name); err != nil {
			t.Errorf("setTheme(%q): %v", name, err)
		}
		for _, state := range []string{"running", "needs", "review", "done", "backlog", "parked"} {
			if _, ok := stateStyles[state]; !ok {
				t.Errorf("theme %q has no style for state %q", name, state)
			}
		}
	}
	if err := setTheme(""); err != nil {
		t.Errorf("setTheme(\"\") should fall back to the default: %v", err)
	}
	if err := setTheme("plasma"); err == nil {
		t.Error("setTheme should refuse an unknown theme")
	}
}

func TestADoneRowStaysReadableOnTheCursorBar(t *testing.T) {
	defer applyTheme(themes[ThemeNames[0]])
	profile := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(profile)
	lipgloss.SetColorProfile(0) // termenv.TrueColor: colours are drawn, so the test sees them
	if err := setTheme("nord"); err != nil {
		t.Fatal(err)
	}
	done := []cell{c("", plain), c("Done", dim), c("a title", dim)}
	if !faded(done) || faded([]cell{c("Running", plain), c("a title", dim)}) || faded([]cell{c("", plain)}) {
		t.Fatal("faded does not tell a done row from the others")
	}
	columns := []column{{name: "BOX"}, {name: "STATE"}, {name: "TITLE"}}
	plainRow := []cell{c("", plain), c("Done", plain), c("a title", plain)}
	under := tableLines(columns, nil, [][]cell{done}, 0, 80)[1]
	want := tableLines(columns, nil, [][]cell{plainRow}, 0, 80)[1]
	if under != want {
		t.Errorf("a done row under the cursor is drawn %q, want the plain text %q", under, want)
	}
}

func TestALitRowKeepsItsGreenOnTheCursorBar(t *testing.T) {
	defer applyTheme(themes[ThemeNames[0]])
	profile := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(profile)
	lipgloss.SetColorProfile(0) // termenv.TrueColor: colours are drawn, so the test sees them
	if err := setTheme("k9s"); err != nil {
		t.Fatal(err)
	}
	columns := []column{{name: "STATE"}}
	under := tableLines(columns, nil, [][]cell{{c("Done", glowStyles[0])}}, 0, 40)[1]
	green, dark := "38;5;"+themes["k9s"].done, "38;5;"+themes["k9s"].cursorText
	if !strings.Contains(under, green) || strings.Contains(under, dark+";") {
		t.Errorf("a lit row under the cursor is drawn %q, want the green %s, not the dark %s", under, green, dark)
	}
}

// The tree of a task that drives others takes a color of its own, the same on every line of it, picked
// from the task at its top; the ids keep the color of the text.
func TestTreeTakesItsTeamColor(t *testing.T) {
	defer applyTheme(themes[ThemeNames[0]])
	profile := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(profile)
	lipgloss.SetColorProfile(0) // termenv.TrueColor: colours are drawn, so the test sees them
	if err := setTheme("bright"); err != nil {
		t.Fatal(err)
	}
	boss, kid, grandkid := task("boss0001", "running", 1), task("kid00001", "running", 2), task("gkid0001", "done", 3)
	kid.Parent, grandkid.Parent = boss.ID, kid.ID
	team := teamStyle("boss0001")
	var lines []string
	for _, row := range format.TreeRows([]api.Task{boss, kid, grandkid}, "state") {
		lines = append(lines, tableLines([]column{{name: "TASK"}}, nil, [][]cell{rowCells(row)[:1]}, -1, 80)[1])
	}
	for i, want := range [][]string{
		{plain.Render("boss0001"), team.Render(" ▾ 1")},
		{team.Render("└─ "), plain.Render("kid00001"), team.Render(" ▾ 1")},
		{team.Render("   └─ "), dim.Render("gkid0001")}, // a task in Done is dimmed, its branch is not
	} {
		for _, piece := range want {
			if !strings.Contains(lines[i], piece) {
				t.Errorf("line %d is drawn %q, want %q in it", i, lines[i], piece)
			}
		}
	}
	if !reflect.DeepEqual(teamStyle("boss0001"), team) || len(teamStyles) != len(themes["bright"].teams) {
		t.Error("a tree does not keep its color")
	}
}
