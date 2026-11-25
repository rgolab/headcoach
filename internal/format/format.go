// Package format turns the figures of the board into short text. The CLI and the TUI share it.
package format

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rouxlabs/headcoach/internal/api"
	"github.com/rouxlabs/headcoach/internal/board"
	"github.com/rouxlabs/headcoach/internal/handoff"
	"github.com/rouxlabs/headcoach/internal/limits"
	"github.com/rouxlabs/headcoach/internal/review"
	"github.com/rouxlabs/headcoach/internal/schedule"
	"github.com/rouxlabs/headcoach/internal/transcript"
	"github.com/rouxlabs/headcoach/internal/usage"
)

// Sorts are the orders a list of tasks can be shown in.
var Sorts = []string{"state", "idle", "cost", "tokens", "ctx"}

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

// SortTasks returns the tasks by state then idle time, by idle time alone with the most recently
// active first, or with the biggest consumer of money, tokens or context first. Pinned tasks come
// before the others whatever the order, the one pinned first on top. The input is left as it is.
func SortTasks(tasks []api.Task, by string) []api.Task {
	out := append([]api.Task(nil), tasks...)
	var less func(a, b *api.Task) bool
	switch by {
	case "idle":
		less = func(a, b *api.Task) bool { return a.Idle < b.Idle }
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
	sort.SliceStable(out, func(i, j int) bool {
		a, b := &out[i], &out[j]
		if (a.PinnedAt > 0) != (b.PinnedAt > 0) {
			return a.PinnedAt > 0
		}
		if a.PinnedAt != b.PinnedAt {
			return a.PinnedAt < b.PinnedAt
		}
		return less(a, b)
	})
	return out
}

// PinMark is what the title of a pinned task starts with. Empty for a task not pinned.
func PinMark(t *api.Task) string {
	if t.PinnedAt > 0 {
		return "▲ "
	}
	return ""
}

// Row is one line of the tree: a task, or one of its child rows.
type Row struct {
	Task  *api.Task
	Agent *api.Agent // nil on the line of the task itself
	Last  bool       // whether the agent is the last child of its task
	// Depth is how many parents the task has above it in the tree: 0 for a task at the top.
	Depth int
	// Tree is what the line starts with: the branches from the task's parents down to it, or on a child
	// row, the lines that go on past it to the tasks below.
	Tree string
	// Kids is how many of the tasks listed the task drives.
	Kids int
	// Team is the task at the top of its tree: the task itself when no parent of it is listed.
	Team string
}

// TreeRows flattens tasks into lines: each task, its sub-agents and background jobs, then the tasks
// it drives, in the same order. A task whose parent is not in the list goes at the top.
func TreeRows(tasks []api.Task, by string) []Row {
	sorted := SortTasks(tasks, by)
	listed := map[string]bool{}
	for i := range sorted {
		listed[sorted[i].ID] = true
	}
	children := map[string][]*api.Task{}
	for i := range sorted {
		if p := sorted[i].Parent; p != "" && p != sorted[i].ID && listed[p] {
			children[p] = append(children[p], &sorted[i])
		}
	}
	var rows []Row
	placed := map[string]bool{}
	// guide is the lines of the parents above that go on past the task; last, whether it is the last
	// task its parent drives.
	var add func(task *api.Task, depth int, guide string, last bool, team string)
	add = func(task *api.Task, depth int, guide string, last bool, team string) {
		if placed[task.ID] {
			return
		}
		placed[task.ID] = true
		if team == "" {
			team = task.ID
		}
		kids := children[task.ID]
		tree, below := guide, guide
		if depth > 0 {
			tree, below = guide+"├─ ", guide+"│  "
			if last {
				tree, below = guide+"└─ ", guide+"   "
			}
		}
		rows = append(rows, Row{Task: task, Depth: depth, Tree: tree, Kids: len(kids), Team: team})
		// Its own child rows sit between it and the tasks it drives: the line to them runs past.
		through := below
		if len(kids) > 0 {
			through += "│"
		}
		through = strings.TrimRight(through, " ")
		for j := range task.Agents {
			rows = append(rows, Row{Task: task, Agent: &task.Agents[j], Last: j == len(task.Agents)-1, Depth: depth,
				Tree: through, Team: team})
		}
		for j, child := range kids {
			add(child, depth+1, below, j == len(kids)-1, team)
		}
	}
	for i := range sorted {
		if p := sorted[i].Parent; p == "" || !listed[p] {
			add(&sorted[i], 0, "", true, "")
		}
	}
	// Tasks that are each other's parents, which the daemon refuses, still show: at the top.
	for i := range sorted {
		add(&sorted[i], 0, "", true, "")
	}
	return rows
}

// Drives marks a task that drives others with how many: "▾ 2". Empty for one that drives none.
func Drives(kids int) string {
	if kids == 0 {
		return ""
	}
	return fmt.Sprintf("▾ %d", kids)
}

// Branch is what a child row of a task starts with: the branch to its sub-agent or background job.
func Branch(row Row) string {
	if row.Last {
		return "└─"
	}
	return "├─"
}

// Who names the author of a log turn in five characters at most: you, agent, tool, or for a message
// the board typed for another agent, the kind of sender its label names: coord, task, other or hc.
func Who(t transcript.Turn) string {
	if t.From != "" {
		switch strings.Fields(t.From)[0] {
		case "coordinator":
			return "coord"
		case "task", "hc":
			return strings.Fields(t.From)[0]
		}
		return "other"
	}
	if t.Who == "you" || t.Who == "agent" {
		return t.Who
	}
	return "tool"
}

// HandoffLines sum up a task's handoff in a few lines: where it is, how old it is, then what it says.
func HandoffLines(h api.Handoff, now time.Time) []string {
	age := Human(max(0, int(float64(now.UnixNano())/1e9-h.UpdatedAt)))
	out := []string{fmt.Sprintf("handoff  %s, written %s ago", h.Path, age)}
	add := func(label, text string) { out = append(out, fmt.Sprintf("  %-9s%s", label, text)) }
	sum := h.Summary
	if sum.Status != "" {
		add("status", sum.Status)
	}
	if len(sum.Running) > 0 {
		add("running", strings.Join(sum.Running, "; "))
	}
	for _, w := range sum.Waiting {
		on := w.On
		if on == "" {
			on = "someone"
		}
		add("waits", "on "+on+": "+w.What)
	}
	if len(sum.Next) > 0 {
		add("next", strings.Join(sum.Next, "; "))
	}
	if queue := QueueCell(sum.Queue); queue != "" {
		if q := sum.Queue; q.Next != "" {
			queue += ", next: " + q.Next
		}
		add("queue", queue)
	}
	return out
}

// Where says where a task under a coordinator stands, in a line: the status of its handoff, else the
// question it asks, else its last line.
func Where(m api.Member) string {
	switch {
	case m.Handoff.Status != "":
		return m.Handoff.Status
	case m.State == "needs":
		return m.Ask
	}
	return m.Last
}

// QueueCell counts the queue of a handoff: "2 open, 3 done", empty without one.
func QueueCell(q handoff.Queue) string {
	if q.Open+q.Done == 0 {
		return ""
	}
	return fmt.Sprintf("%d open, %d done", q.Open, q.Done)
}

// OnYouWhat says what waits on you, after the kind of wait: a question, a change in Review, or a line of
// a handoff file.
func OnYouWhat(o api.OnYou) string {
	switch o.Kind {
	case "needs":
		return "asks: " + o.What
	case "review":
		return "in Review: " + o.What
	}
	return o.What
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

// TimerCell is the time left on a task's timer, or before it is deleted after a ✗, whichever comes
// first. Nothing when it has neither.
func TimerCell(left, deleteLeft int) string {
	if deleteLeft > 0 && (left <= 0 || deleteLeft <= left) {
		return "✗ " + Human(deleteLeft)
	}
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
