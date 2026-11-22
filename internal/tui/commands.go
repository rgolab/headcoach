package tui

import (
	"errors"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/humblebundledore/headcoach/internal/install"
	"github.com/humblebundledore/headcoach/internal/update"
	"github.com/humblebundledore/headcoach/internal/version"
)

// commandNames are what the : command line knows, in the order its help lists them.
var commandNames = []string{"update", "restart", "columns", "help", "q"}

const commandHelp = `update    install the latest release, then the board reloads itself
restart   restart the daemon
columns   show, hide and reorder the columns of the board
help      this list
q         quit the board`

// Seams for tests: what :update and :restart do outside the board.
var (
	checkRelease   = update.Check
	installRelease = update.Install
	restartDaemon  = func() error {
		if !install.ServiceInstalled() {
			return errors.New("no daemon service is installed: restart your hc serve by hand")
		}
		return install.ServiceRestart()
	}
)

// command runs what was typed on the : line.
func (m model) command(text string) (tea.Model, tea.Cmd) {
	switch strings.ToLower(strings.Fields(text)[0]) {
	case "q", "quit":
		return m, tea.Quit
	case "help":
		m.note("Commands (any key closes)", commandHelp, nil)
		return m, nil
	case "columns":
		m.view, m.colCursor = viewColumns, 0
		return m, nil
	case "restart":
		m.say("Restarting the daemon", false)
		return m, func() tea.Msg {
			if err := restartDaemon(); err != nil {
				return doneMsg{err: err}
			}
			return doneMsg{notice: "Daemon restarted"}
		}
	case "update":
		if m.checking {
			m.say("Still looking for a newer release", false)
			return m, nil
		}
		m.checking = true
		m.say("Looking for a newer release", false)
		return m, func() tea.Msg {
			rel, newer, err := checkRelease(version.Version)
			return checkedMsg{rel, newer, err}
		}
	}
	m.say("Unknown command "+text+". Known: "+strings.Join(commandNames, ", "), true)
	return m, nil
}

// checked shows what :update found, and installs it on a yes.
func (m model) checked(msg checkedMsg) (tea.Model, tea.Cmd) {
	m.checking = false
	switch {
	case errors.Is(msg.err, update.ErrSource):
		m.note("Update", "This hc, "+version.Version+", is built from source. Update it the same way:\n\n"+
			"  git pull && make install && hc restart\n\nThe board reloads itself once the daemon runs the new build.", nil)
		return m, nil
	case msg.err != nil:
		m.say("Could not look for a release: "+msg.err.Error(), true)
		return m, nil
	case !msg.newer:
		m.say("hc "+version.Version+" is the latest release", false)
		return m, nil
	}
	tag := msg.rel.Tag
	// A y or n typed into the input line, or meant for another question, must not answer this one; nor
	// may it open over ticked tasks.
	if m.asking != "" || m.confirm != nil || m.selecting {
		m.say(tag+" is out: press u once you are done to install it", false)
		return m, nil
	}
	text := "Install " + tag + ", checked against SHA256SUMS?  y / n"
	if notes := strings.TrimSpace(msg.rel.Notes); notes != "" {
		text = notes + "\n\n" + text
	}
	m.note("Update to "+tag, text, func() tea.Cmd {
		// Say it right away: the download takes a moment and the board must not look idle meanwhile.
		installing := func() tea.Msg { return doneMsg{notice: "Installing " + tag} }
		return tea.Sequence(installing, func() tea.Msg {
			if _, err := installRelease(tag, version.Version); err != nil {
				return doneMsg{err: err}
			}
			if err := restartDaemon(); err != nil {
				return doneMsg{err: errors.New(tag + " is installed, but " + err.Error())}
			}
			return doneMsg{notice: tag + " installed: the board reloads itself"}
		})
	})
	return m, nil
}
