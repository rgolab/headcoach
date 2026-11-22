package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// palette is the set of colors of one theme. A color is an ANSI number, a 256-color number or a
// hex value; lipgloss degrades it to what the terminal can show. Empty text keeps the terminal's
// default; empty muted means faint.
type palette struct {
	text, muted, accent, alarm           string
	running, needs, review, done, parked string
	warm, selected                       string
	// heading colours the table headings; empty falls back on the text color, or dim without one.
	heading string
	// cursorText recolours the text on the cursor bar; a light bar needs a dark one.
	cursorText string
}

// ThemeNames lists the themes, the default first.
var ThemeNames = []string{"terminal", "bright", "nord", "k9s"}

var themes = map[string]palette{
	// terminal takes whatever your terminal theme gives the 16 ANSI colors.
	"terminal": {
		accent: "6", alarm: "1",
		running: "4", needs: "3", review: "5", done: "2", parked: "6",
		warm: "3", selected: "237",
	},
	// bright does not depend on the terminal theme: vivid colors and a blue cursor bar.
	"bright": {
		text: "252", muted: "245", accent: "81", alarm: "203",
		running: "75", needs: "215", review: "204", done: "113", parked: "80",
		warm: "221", selected: "25",
	},
	// nord is the muted blue palette of nordtheme.com.
	"nord": {
		text: "#D8DEE9", muted: "#616E88", accent: "#88C0D0", alarm: "#BF616A",
		running: "#81A1C1", needs: "#EBCB8B", review: "#B48EAD", done: "#A3BE8C", parked: "#8FBCBB",
		warm: "#EBCB8B", selected: "#434C5E",
	},
	// k9s copies the default k9s skin: orange labels, cyan rows, a light blue cursor bar.
	"k9s": {
		text: "45", muted: "245", accent: "214", alarm: "196", heading: "255", cursorText: "16",
		running: "39", needs: "214", review: "201", done: "42", parked: "51",
		warm: "220", selected: "117",
	},
}

// active is the palette of the current theme, for the few spots that mix their own style.
var active palette

func init() { applyTheme(themes[ThemeNames[0]]) }

// setTheme switches every style of the board to the named theme.
func setTheme(name string) error {
	if name == "" {
		name = ThemeNames[0]
	}
	p, known := themes[name]
	if !known {
		return fmt.Errorf("unknown theme %q. Known: %s", name, strings.Join(ThemeNames, ", "))
	}
	applyTheme(p)
	return nil
}

func applyTheme(p palette) {
	fg := func(color string) lipgloss.Style {
		style := lipgloss.NewStyle()
		if color != "" {
			style = style.Foreground(lipgloss.Color(color))
		}
		return style
	}
	active = p
	plain = fg(p.text)
	bold = fg(p.text).Bold(true)
	dim = fg(p.muted)
	if p.muted == "" {
		dim = lipgloss.NewStyle().Faint(true)
	}
	heading = bold
	if p.heading != "" {
		heading = fg(p.heading).Bold(true)
	} else if p.text == "" {
		heading = dim.Bold(true)
	}
	accent = fg(p.accent).Bold(true)
	alarm = fg(p.alarm).Bold(true)
	stateStyles = map[string]lipgloss.Style{
		"running": fg(p.running),
		"waiting": dim, // running, but only waiting on its sub-agents: nothing to look at
		"needs":   fg(p.needs).Bold(true),
		"review":  fg(p.review),
		"done":    dim, // over: grey wherever it shows, like the rows of done tasks
		"backlog": dim,
		"parked":  fg(p.parked),
	}
	levelStyles = map[string]lipgloss.Style{
		"empty": dim,
		"ok":    fg(p.done),
		"warm":  fg(p.warm),
		"hot":   alarm,
	}
	whoStyles = map[string]lipgloss.Style{
		"you":   accent,
		"agent": fg(p.done).Bold(true),
		"tool":  dim,
	}
	box = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(p.accent)).Padding(0, 1)
	selected = lipgloss.Color(p.selected)
	glowStyles = [2]lipgloss.Style{fg(p.done).Bold(true), fg(p.done)}
}
