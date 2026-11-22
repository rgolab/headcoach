// Package format turns the figures of the board into short text. The CLI and the TUI share it.
package format

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/humblebundledore/headcoach/internal/api"
	"github.com/humblebundledore/headcoach/internal/board"
	"github.com/humblebundledore/headcoach/internal/limits"
	"github.com/humblebundledore/headcoach/internal/review"
	"github.com/humblebundledore/headcoach/internal/schedule"
	"github.com/humblebundledore/headcoach/internal/usage"
)

// Sorts are the orders a list of tasks can be shown in.
var Sorts = []string{"state", "cost", "tokens", "ctx"}

// order is the rank of each state in a list: what needs you comes first.
var order = map[string]int{"needs": 0, "running": 1, "review": 2, "backlog": 3, "parked": 4, "done": 5}

// Human writes a duration in four characters or so: 45s, 12m, 2h05, 3d.
func Human(seconds int) string {
	switch {
	case seconds < 60:
		return fmt.Sprintf("%ds", seconds)
	case seconds < 3600:
		return fmt.Sprintf("%dm", seconds/60)
	case seconds < 86400:
		return fmt.Sprintf("%dh%02d", seconds/3600, seconds%3600/60)
	}
	return fmt.Sprintf("%dd", seconds/86400)
}

// Count writes a token count in three characters or so: 950, 12k, 1.4M.
func Count(n int) string {
	for _, unit := range []struct {
		limit int
		name  string
	}{{1_000_000_000, "G"}, {1_000_000, "M"}, {1_000, "k"}} {
		if n >= unit.limit {
			value := float64(n) / float64(unit.limit)
			if value < 10 {
				return fmt.Sprintf("%.1f%s", value, unit.name)
			}
			return fmt.Sprintf("%.0f%s", value, unit.name)
		}
	}
	return fmt.Sprint(n)
}

// TokenCells gives the in, out and cache cells of a table row. Cache adds what was read from it and
// what was written to it. All three are empty when there is nothing to count.
func TokenCells(tokens *usage.Tokens) (in, out, cache string) {
	if tokens == nil {
		return "", "", ""
	}
	return Count(tokens.In), Count(tokens.Out), Count(tokens.CacheRead + tokens.CacheWrite)
}

// TokenLine writes the four counts in full.
func TokenLine(tokens usage.Tokens) string {
	return fmt.Sprintf("in %s  out %s  cache read %s  cache write %s",
		Count(tokens.In), Count(tokens.Out), Count(tokens.CacheRead), Count(tokens.CacheWrite))
}

// Money writes a price in four characters or so: $0.42, $12.4, $123. Empty when it is zero.
func Money(usd float64) string {
	switch {
	case usd == 0:
		return ""
	case usd < 10:
		return fmt.Sprintf("$%.2f", usd)
	case usd < 100:
		return fmt.Sprintf("$%.1f", usd)
	}
	return fmt.Sprintf("$%.0f", usd)
}

// SortTasks returns the tasks by state then idle time, or with the biggest consumer of money, tokens
// or context first. The input is left as it is.
func SortTasks(tasks []api.Task, by string) []api.Task {
	out := append([]api.Task(nil), tasks...)
	var less func(a, b *api.Task) bool
	switch by {
	case "cost":
		less = func(a, b *api.Task) bool { return a.Cost > b.Cost }
	case "tokens":
		less = func(a, b *api.Task) bool { return a.Tokens.Total() > b.Tokens.Total() }
	case "ctx":
		less = func(a, b *api.Task) bool { return a.Context.Tokens > b.Context.Tokens }
	default:
		less = func(a, b *api.Task) bool {
			if order[a.State] != order[b.State] {
				return order[a.State] < order[b.State]
			}
			return a.Idle < b.Idle
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return less(&out[i], &out[j]) })
	return out
}

// Row is one line of the tree: a task, or one of its child rows.
type Row struct {
	Task  *api.Task
	Agent *api.Agent // nil on the line of the task itself
	Last  bool       // whether the agent is the last child of its task
}

// TreeRows flattens tasks and their child rows into lines.
func TreeRows(tasks []api.Task, by string) []Row {
	sorted := SortTasks(tasks, by)
	var rows []Row
	for i := range sorted {
		task := &sorted[i]
		rows = append(rows, Row{Task: task})
		for j := range task.Agents {
			rows = append(rows, Row{Task: task, Agent: &task.Agents[j], Last: j == len(task.Agents)-1})
		}
	}
	return rows
}

// ContextLevel tells how full the context window is: empty, ok, warm or hot. The colours of the gauge
// hang on this.
func ContextLevel(c api.Context) string {
	switch {
	case c.Tokens == 0:
		return "empty"
	case c.Pct >= 80:
		return "hot"
	case c.Pct >= 50:
		return "warm"
	}
	return "ok"
}

// ContextCell writes the context gauge of a row: 145k 72%.
func ContextCell(c api.Context) string {
	if c.Tokens == 0 {
		return ""
	}
	return fmt.Sprintf("%s %d%%", Count(c.Tokens), c.Pct)
}

// LimitWarning is empty unless the window runs out before it resets.
func LimitWarning(l limits.Entry) string {
	if l.LimitIn == nil {
		return ""
	}
	when := "reached"
	if *l.LimitIn > 0 {
		when = "in about " + Human(*l.LimitIn)
	}
	return fmt.Sprintf("%s %s limit %s", l.Profile, l.Label, when)
}

// ansi holds the terminal colour of each state and level.
var ansi = map[string]string{
	"needs": "1;33", "running": "34", "review": "35", "parked": "36", "done": "2", "backlog": "2",
	"ok": "32", "warm": "33", "hot": "1;31", "empty": "2", "dim": "2",
}

// Paint colours text for a terminal when on is true and the style is known.
func Paint(text, style string, on bool) string {
	if code, known := ansi[style]; on && known {
		return "\033[" + code + "m" + text + "\033[0m"
	}
	return text
}

// When writes a moment of the coming or past week: "today 08:00", "tomorrow 08:00", "Mon 08:00".
// Zero is a dash.
func When(unix float64, now time.Time) string {
	if unix == 0 {
		return "-"
	}
	t := time.Unix(int64(unix), 0).In(now.Location())
	day := func(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()) }
	switch int(day(t).Sub(day(now)).Hours()+0.5) / 24 {
	case 0:
		return "today " + t.Format("15:04")
	case 1:
		return "tomorrow " + t.Format("15:04")
	case -1:
		return "yesterday " + t.Format("15:04")
	}
	if d := day(t).Sub(day(now)); d < 7*24*time.Hour && d > -7*24*time.Hour {
		return t.Format("Mon 15:04")
	}
	return t.Format("Jan 2 15:04")
}

// ScheduleDays writes the days of a schedule for a table: daily, weekdays, or Mon,Fri. A cron line
// has its days in it: -.
func ScheduleDays(at, days string) string {
	if schedule.IsCron(at) {
		return "-"
	}
	if days == "daily" || days == "weekdays" || days == "weekends" {
		return days
	}
	parts := strings.Split(days, ",")
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, ",")
}

// LastRun writes how the last run of a schedule went: when, and the state of its task.
func LastRun(sc api.Schedule, now time.Time) string {
	if sc.LastRun == 0 {
		return "never"
	}
	out := When(sc.LastRun, now)
	switch {
	case sc.LastError != "":
		out += " failed: " + sc.LastError
	case sc.LastState != "":
		out += " " + sc.LastState
	}
	return out
}

// Marked puts ↻ before the title of a task a schedule started, so a terminal board tells its runs apart.
func Marked(title, source string) string {
	if source == "schedule" {
		return "↻ " + title
	}
	return title
}

// ScheduleCell names the schedule that started a task by its short id, or nothing: a task started
// by hand, or a run from before tasks kept their schedule.
func ScheduleCell(schedule string) string {
	if schedule == "" {
		return ""
	}
	return board.Short(schedule)
}

// Timed puts ⏱ and the time left before a title when the task carries a timer: the board closes it then.
func Timed(title string, left int) string {
	if left <= 0 {
		return title
	}
	return "⏱ " + Human(left) + " " + title
}

// TimerCell is the time left on a task's timer, or nothing when it has none.
func TimerCell(left int) string {
	if left <= 0 {
		return ""
	}
	return Human(left)
}

// modelFamilies are the model families a model column names, for a name that carries one.
var modelFamilies = []string{"opus", "sonnet", "haiku", "fable"}

// ShortModel is a model's family, such as opus for claude-opus-4-5 or opus[1m]. A name of no known
// family stays as it is, without its claude- prefix.
func ShortModel(name string) string {
	lower := strings.ToLower(name)
	for _, family := range modelFamilies {
		if strings.Contains(lower, family) {
			return family
		}
	}
	return strings.TrimPrefix(lower, "claude-")
}

// Closed tells a task should have a session and has none: its tmux pane is gone while it works,
// waits on you or waits for review.
func Closed(t *api.Task) bool {
	return !t.Live && (t.State == "running" || t.State == "needs" || t.State == "review")
}

// ModelCell is the model a task runs on: the one it was started with, else the one its latest
// session answered with. Empty before either is known.
func ModelCell(chosen, reported string) string {
	if chosen != "" {
		return ShortModel(chosen)
	}
	return ShortModel(reported)
}

// EffortCell is the effort level a task runs at: the one it was started with, else the one its latest
// session reports. Empty before either is known.
func EffortCell(chosen, reported string) string {
	if chosen != "" {
		return chosen
	}
	return reported
}

// ChangeCell writes the size of a change: its commits, then its files and lines. "-" outside a checkout.
func ChangeCell(c *review.Change) string {
	switch {
	case c == nil || c.From == "" && !c.Dirty:
		return "-"
	case c.Commits == 0 && c.Dirty:
		return "uncommitted"
	case c.Commits == 0:
		return "no commits"
	}
	return fmt.Sprintf("%s, %s +%d -%d", Counted(c.Commits, "commit"), Counted(c.Files, "file"), c.Added, c.Deleted)
}

// PRLabel is how a pull or merge request is written: #12 on GitHub, !12 on GitLab.
func PRLabel(url string) string {
	cut := strings.LastIndexByte(url, '/')
	if cut < 0 {
		return url
	}
	if strings.Contains(url, "/-/merge_requests/") {
		return "!" + url[cut+1:]
	}
	return "#" + url[cut+1:]
}

// DeliveryCell says where a change stands on its way out.
func DeliveryCell(it api.ReviewItem) string {
	c := it.Change
	switch it.Delivery {
	case "pr":
		if it.PRState != "" {
			return "PR " + PRLabel(it.PR) + " " + it.PRState
		}
		return "PR " + PRLabel(it.PR)
	case "push":
		switch {
		case c.Dirty:
			return "uncommitted"
		case c.Unpushed < 0:
			return "no upstream"
		case c.Unpushed > 0:
			return fmt.Sprintf("%d to push", c.Unpushed)
		}
		return "pushed"
	case "branch":
		return "no PR yet"
	}
	return "-"
}

// Counted writes a count with its noun, singular for one: 1 task, 2 tasks.
func Counted(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// ReviewMetrics writes how the review queue has been doing.
func ReviewMetrics(m api.ReviewMetrics) string {
	wait := "no task left Review"
	if m.Reviews > 0 {
		wait = fmt.Sprintf("a task waited %s in Review on average (%s)", Human(m.AvgWait), Counted(m.Reviews, "review"))
	}
	cost := "no task Done"
	if m.Delivered > 0 {
		cost = fmt.Sprintf("%s per task Done (%d)", Money(m.CostPerChange), m.Delivered)
		if m.CostPerChange == 0 {
			cost = fmt.Sprintf("no cost reported for %s Done", Counted(m.Delivered, "task"))
		}
	}
	return fmt.Sprintf("Last %d days: %s, %s", m.Days, wait, cost)
}
