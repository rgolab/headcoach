package format

import (
	"reflect"
	"testing"

	"github.com/humblebundledore/headcoach/internal/api"
	"github.com/humblebundledore/headcoach/internal/limits"
	"github.com/humblebundledore/headcoach/internal/review"
	"github.com/humblebundledore/headcoach/internal/store"
	"github.com/humblebundledore/headcoach/internal/transcript"
	"github.com/humblebundledore/headcoach/internal/usage"
)

func TestHuman(t *testing.T) {
	for seconds, want := range map[int]string{
		0: "0s", 1: "1s", 59: "59s", 60: "1m", 61: "1m", 3599: "59m", 3600: "1h00", 3660: "1h01", 7325: "2h02",
		86399: "23h59", 86400: "1d", 172799: "1d", 864000: "10d",
	} {
		if got := Human(seconds); got != want {
			t.Errorf("Human(%d) = %q, want %q", seconds, got, want)
		}
	}
}

func TestCountAndMoney(t *testing.T) {
	for n, want := range map[int]string{
		0: "0", 999: "999", 1000: "1.0k", 12_345: "12k", 1_400_000: "1.4M", 40_000_000: "40M", 2_500_000_000: "2.5G",
	} {
		if got := Count(n); got != want {
			t.Errorf("Count(%d) = %q, want %q", n, got, want)
		}
	}
	for usd, want := range map[float64]string{0: "", 0.416: "$0.42", 12.44: "$12.4", 123.4: "$123"} {
		if got := Money(usd); got != want {
			t.Errorf("Money(%v) = %q, want %q", usd, got, want)
		}
	}
	in, out, cache := TokenCells(&usage.Tokens{In: 1500, Out: 20, CacheRead: 900_000, CacheWrite: 100_000})
	if in != "1.5k" || out != "20" || cache != "1.0M" {
		t.Errorf("TokenCells = %q %q %q", in, out, cache)
	}
	if in, out, cache := TokenCells(nil); in+out+cache != "" {
		t.Error("no tokens should give empty cells")
	}
}

func task(id, state string, idle int) api.Task {
	return api.Task{Task: store.Task{ID: id, State: state}, Idle: idle}
}

func ids(tasks []api.Task) []string {
	var out []string
	for _, t := range tasks {
		out = append(out, t.ID)
	}
	return out
}

func TestSortTasks(t *testing.T) {
	tasks := []api.Task{
		task("done1", "done", 1), task("back1", "backlog", 5), task("rev1", "review", 9), task("run2", "running", 50),
		task("park1", "parked", 1), task("run1", "running", 2), task("need2", "needs", 30), task("need1", "needs", 3),
	}
	want := []string{"need1", "need2", "run1", "run2", "rev1", "back1", "park1", "done1"}
	if got := ids(SortTasks(tasks, "state")); !reflect.DeepEqual(got, want) {
		t.Errorf("by state = %v", got)
	}
	if tasks[0].ID != "done1" {
		t.Error("the input was reordered")
	}
	if len(SortTasks(nil, "state")) != 0 {
		t.Error("no tasks should sort to no tasks")
	}

	small, big, none := task("small", "running", 1), task("big", "running", 1), task("none", "needs", 1)
	small.Tokens, small.Context, small.Cost = usage.Tokens{Out: 10}, api.Context{Tokens: 150_000}, 1
	big.Tokens, big.Context, big.Cost = usage.Tokens{Out: 900}, api.Context{Tokens: 20_000}, 9
	used := []api.Task{small, big, none}
	for by, want := range map[string][]string{
		"tokens": {"big", "small", "none"}, "ctx": {"small", "big", "none"}, "cost": {"big", "small", "none"},
		"state": {"none", "small", "big"},
	} {
		if got := ids(SortTasks(used, by)); !reflect.DeepEqual(got, want) {
			t.Errorf("by %s = %v, want %v", by, got, want)
		}
	}
}

func TestTreeRows(t *testing.T) {
	done, run, need := task("done1", "done", 1), task("run1", "running", 2), task("need1", "needs", 3)
	done.Agents = []api.Agent{{Name: "a3"}}
	run.Agents = []api.Agent{{Name: "a1"}, {Name: "a2"}}
	var got []string
	for _, row := range TreeRows([]api.Task{done, run, need}, "state") {
		line := row.Task.ID
		if row.Agent != nil {
			line += " " + row.Agent.Name
		}
		if row.Last {
			line += " last"
		}
		got = append(got, line)
	}
	want := []string{"need1", "run1", "run1 a1", "run1 a2 last", "done1", "done1 a3 last"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %v", got)
	}
}

func TestContextAndPaint(t *testing.T) {
	for pct, want := range map[int]string{0: "ok", 49: "ok", 50: "warm", 79: "warm", 80: "hot", 100: "hot"} {
		if got := ContextLevel(api.Context{Tokens: 1000, Window: 200_000, Pct: pct}); got != want {
			t.Errorf("level at %d%% = %q", pct, got)
		}
	}
	if ContextLevel(api.Context{}) != "empty" || ContextCell(api.Context{}) != "" {
		t.Error("an empty context should show nothing")
	}
	if got := ContextCell(api.Context{Tokens: 145_000, Window: 200_000, Pct: 72}); got != "145k 72%" {
		t.Errorf("cell = %q", got)
	}
	if Paint("x", "done", true) != "\033[2mx\033[0m" {
		t.Error("the Done state is not dim")
	}
	if Paint("x", "hot", true) != "\033[1;31mx\033[0m" || Paint("x", "hot", false) != "x" || Paint("x", "unknown", true) != "x" {
		t.Error("Paint is wrong")
	}
}

func TestLimitTexts(t *testing.T) {
	quiet := limits.Entry{Profile: "work", Label: "5h", Pct: 34, ResetsIn: 7800}
	soon, reached := 4800, 0
	if got := LimitWarning(quiet); got != "" {
		t.Errorf("quiet = %q", got)
	}
	quiet.LimitIn = &soon
	if got := LimitWarning(quiet); got != "work 5h limit in about 1h20" {
		t.Errorf("warning = %q", got)
	}
	quiet.LimitIn = &reached
	if got := LimitWarning(quiet); got != "work 5h limit reached" {
		t.Errorf("warning = %q", got)
	}
}

func TestReviewCells(t *testing.T) {
	for _, tc := range []struct {
		change *review.Change
		want   string
	}{
		{nil, "-"},
		{&review.Change{}, "-"},
		{&review.Change{Dirty: true}, "uncommitted"},
		{&review.Change{From: "a"}, "no commits"},
		{&review.Change{From: "a", Commits: 1, Files: 2, Added: 30, Deleted: 4}, "1 commit, 2 files +30 -4"},
		{&review.Change{From: "a", Commits: 3, Files: 1, Added: 1}, "3 commits, 1 file +1 -0"},
	} {
		if got := ChangeCell(tc.change); got != tc.want {
			t.Errorf("ChangeCell(%+v) = %q, want %q", tc.change, got, tc.want)
		}
	}
	for _, tc := range []struct {
		item api.ReviewItem
		want string
	}{
		{api.ReviewItem{}, "-"},
		{api.ReviewItem{Delivery: "pr", PR: "https://github.com/o/r/pull/12"}, "PR #12"},
		{api.ReviewItem{Delivery: "pr", PR: "https://gitlab.com/g/p/-/merge_requests/7"}, "PR !7"},
		{api.ReviewItem{Delivery: "pr", PR: "https://github.com/o/r/pull/12", PRState: "open"}, "PR #12 open"},
		{api.ReviewItem{Delivery: "branch"}, "no PR yet"},
		{api.ReviewItem{Delivery: "push", Change: &review.Change{Unpushed: 2}}, "2 to push"},
		{api.ReviewItem{Delivery: "push", Change: &review.Change{Unpushed: -1}}, "no upstream"},
		{api.ReviewItem{Delivery: "push", Change: &review.Change{Unpushed: 1, Dirty: true}}, "uncommitted"},
		{api.ReviewItem{Delivery: "push", Change: &review.Change{}}, "pushed"},
	} {
		if got := DeliveryCell(tc.item); got != tc.want {
			t.Errorf("DeliveryCell(%+v) = %q, want %q", tc.item, got, tc.want)
		}
	}
	for m, want := range map[api.ReviewMetrics]string{
		{Days: 7}: "Last 7 days: no task left Review, no task Done",
		{Days: 7, Reviews: 4, AvgWait: 3900, Delivered: 2, CostPerChange: 1.5}: "Last 7 days: a task waited 1h05 in Review on average (4 reviews), $1.50 per task Done (2)",
		{Days: 7, Delivered: 3}:                          "Last 7 days: no task left Review, no cost reported for 3 tasks Done",
		{Days: 7, Reviews: 1, AvgWait: 30, Delivered: 1}: "Last 7 days: a task waited 30s in Review on average (1 review), no cost reported for 1 task Done",
	} {
		if got := ReviewMetrics(m); got != want {
			t.Errorf("ReviewMetrics(%+v) = %q, want %q", m, got, want)
		}
	}
}

func TestScheduleCell(t *testing.T) {
	for schedule, want := range map[string]string{"sched123456": "sched123", "": ""} {
		if got := ScheduleCell(schedule); got != want {
			t.Errorf("ScheduleCell(%q) = %q, want %q", schedule, got, want)
		}
	}
}

func TestModelCell(t *testing.T) {
	for _, c := range []struct{ chosen, reported, want string }{
		{"", "", ""},
		{"sonnet", "claude-opus-4-5-20251101", "sonnet"},
		{"", "claude-opus-4-5-20251101", "opus"},
		{"opus[1m]", "", "opus"},
		{"Claude-Haiku-4-5", "", "haiku"},
		{"claude-fable-5-2", "", "fable"},
		{"claude-next-1", "", "next-1"},
	} {
		if got := ModelCell(c.chosen, c.reported); got != c.want {
			t.Errorf("ModelCell(%q, %q) = %q, want %q", c.chosen, c.reported, got, c.want)
		}
	}
}

func TestEffortCell(t *testing.T) {
	for _, c := range [][3]string{{"", "", ""}, {"low", "high", "low"}, {"", "max", "max"}} {
		if got := EffortCell(c[0], c[1]); got != c[2] {
			t.Errorf("EffortCell(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}

func TestTimerCell(t *testing.T) {
	for left, want := range map[int]string{0: "", -1: "", 45: "45s", 420: "7m"} {
		if got := TimerCell(left); got != want {
			t.Errorf("TimerCell(%d) = %q, want %q", left, got, want)
		}
	}
}

func TestTimed(t *testing.T) {
	for _, c := range []struct {
		title string
		left  int
		want  string
	}{{"run", 0, "run"}, {"run", 420, "⏱ 7m run"}, {"↻ run", 45, "⏱ 45s ↻ run"}, {"run", -1, "run"}} {
		if got := Timed(c.title, c.left); got != c.want {
			t.Errorf("Timed(%q, %d) = %q, want %q", c.title, c.left, got, c.want)
		}
	}
}

func TestWho(t *testing.T) {
	for _, c := range []struct {
		turn transcript.Turn
		want string
	}{
		{transcript.Turn{Who: "you"}, "you"},
		{transcript.Turn{Who: "agent"}, "agent"},
		{transcript.Turn{Who: "tool"}, "tool"},
		{transcript.Turn{Who: "something new"}, "tool"},
		{transcript.Turn{Who: "you", From: "coordinator 1a2b3c4d"}, "coord"},
		{transcript.Turn{Who: "you", From: "task 1a2b3c4d"}, "task"},
		{transcript.Turn{Who: "you", From: "an agent"}, "other"},
		{transcript.Turn{Who: "you", From: "hc"}, "hc"},
	} {
		if got := Who(c.turn); got != c.want {
			t.Errorf("Who(%+v) = %q, want %q", c.turn, got, c.want)
		}
	}
}
