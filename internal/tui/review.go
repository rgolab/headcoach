package tui

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/humblebundledore/headcoach/internal/api"
	"github.com/humblebundledore/headcoach/internal/board"
	"github.com/humblebundledore/headcoach/internal/format"
)

// reviewMsg carries the review queue, after a poll.
type reviewMsg struct {
	queue api.Review
	err   error
}

// diffDoneMsg says the terminal is back from the diff.
type diffDoneMsg struct{ err error }

var reviewColumns = []column{
	{"TASK", false}, {"WAITED", true}, {"REPO", false}, {"BRANCH", false}, {"CHANGE", false}, {"TESTS", false},
	{"DELIVERY", false}, {"REVIEWER", false}, {"TITLE", false},
}

const reviewHelp = `j / k, arrows   move the cursor
g / G           go to the top or the bottom
o               open the change: its pull or merge request in the browser, or else its diff
                in git's pager, the commits since the task started
enter           show the task on the board
esc or q        back to the board

The tasks in Review wait on you, the one waiting longest first. TESTS is the last test
command the task ran, REVIEWER the verdict of its last reviewer sub-agent.`

// openURL opens an address in the browser. Tests replace it.
var openURL = func(url string) {
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	_ = exec.Command(opener, url).Start()
}

func (m model) loadReview() tea.Cmd {
	c := m.client
	return func() tea.Msg {
		var queue api.Review
		err := c.Call("GET", "/api/review", nil, &queue)
		return reviewMsg{queue: queue, err: err}
	}
}

func (m model) currentReview() *api.ReviewItem {
	if m.reviewCursor < 0 || m.reviewCursor >= len(m.review.Items) {
		return nil
	}
	return &m.review.Items[m.reviewCursor]
}

// reviewTarget is what o opens for a task: the address of its pull request, or the git command that
// shows its diff. Both are empty when there is nothing to show.
func reviewTarget(it api.ReviewItem) (url string, diff []string) {
	c := it.Change
	switch {
	case it.PR != "":
		return it.PR, nil
	case c == nil:
		return "", nil
	case c.Commits > 0 && c.From != "":
		return "", []string{"git", "-C", it.Cwd, "log", "--reverse", "--stat", "-p", c.From + ".." + c.To}
	case c.Dirty:
		return "", []string{"git", "-C", it.Cwd, "diff", "HEAD"}
	}
	return "", nil
}

// reviewKey handles a key on the review queue.
func (m model) reviewKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "Q":
		return m, tea.Quit
	case "esc", "q":
		m.view = viewBoard
		return m, nil
	case "j", "down":
		m.reviewCursor = min(m.reviewCursor+1, len(m.review.Items)-1)
		return m, nil
	case "k", "up":
		m.reviewCursor = max(m.reviewCursor-1, 0)
		return m, nil
	case "g", "home":
		m.reviewCursor = 0
		return m, nil
	case "G", "end":
		m.reviewCursor = max(0, len(m.review.Items)-1)
		return m, nil
	case "?":
		m.note("Review keys (any key closes)", reviewHelp, nil)
		return m, nil
	}
	it := m.currentReview()
	if it == nil {
		return m, nil
	}
	switch key {
	case "enter":
		m.view, m.jump = viewBoard, it.ID
		m.rebuild()
		return m, nil
	case "o":
		url, diff := reviewTarget(*it)
		switch {
		case url != "":
			openURL(url)
			m.say("Opened "+url, false)
		case diff != nil:
			cmd := exec.Command(diff[0], diff[1:]...)
			if os.Getenv("LESS") == "" {
				// git's default makes less quit at once on a short diff: the board would come back before you read it.
				cmd.Env = append(os.Environ(), "LESS=R")
			}
			return m, tea.ExecProcess(cmd, func(err error) tea.Msg { return diffDoneMsg{err} })
		default:
			m.say(it.Title+" has no change to show", true)
		}
	}
	return m, nil
}

// reviewCells are the cells of one row of the review queue.
func reviewCells(it api.ReviewItem) []cell {
	tests := c("-", dim)
	switch it.Tests {
	case "passed":
		tests = c("passed", levelStyles["ok"])
	case "failed":
		tests = c("failed", alarm)
	}
	delivery := format.DeliveryCell(it)
	deliveryStyle := plain
	if delivery == "-" {
		deliveryStyle = dim
	}
	verdict := c("-", dim)
	if it.Verdict != "" {
		verdict = c(board.Clip(it.Verdict, 40), plain)
	}
	return []cell{
		c(it.Short, plain), c(format.Human(it.Waited), bold), c(board.Clip(it.Repo, 20), plain),
		c(board.Clip(it.Branch, 24), plain), c(format.ChangeCell(it.Change), plain), tests, c(delivery, deliveryStyle),
		verdict, c(board.Clip(format.Timed(format.Marked(it.Title, it.Source), it.TimerLeft), 60), plain),
	}
}

func (m model) reviewView() string {
	// The rows scroll with the cursor.
	height := m.tableHeight()
	top := max(0, m.reviewCursor-height+1)
	var rows [][]cell
	for _, it := range m.review.Items[top:min(len(m.review.Items), top+height)] {
		rows = append(rows, reviewCells(it))
	}
	table := tableLines(reviewColumns, m.reviewWidths, rows, m.reviewCursor-top, max(10, m.width-2))
	if len(m.review.Items) == 0 {
		table = append(table, dim.Render(" Nothing waits on you in Review."))
	}
	title := []cell{c("review", stateStyles["review"].Bold(true)), c(fmt.Sprintf("[%d]", len(m.review.Items)), plain)}
	return m.frame(title, table, []cell{c(format.ReviewMetrics(m.review.Metrics), dim)})
}
