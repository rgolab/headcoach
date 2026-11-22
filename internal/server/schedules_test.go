package server

import (
	"testing"
	"time"

	"github.com/humblebundledore/headcoach/internal/store"
)

// monday is a Monday at hour:minute, days later when days is set, local time.
func monday(days, hour, minute int) time.Time {
	return time.Date(2030, 1, 7+days, hour, minute, 0, 0, time.Local)
}

func (f *fixture) schedule(id string) store.Schedule {
	f.t.Helper()
	sc, err := f.st.Schedule(id)
	if err != nil || sc == nil {
		f.t.Fatalf("schedule %s: %v", id, err)
	}
	return *sc
}

func (f *fixture) runDue(now time.Time) {
	f.t.Helper()
	if err := f.srv.RunDue(now); err != nil {
		f.t.Fatal(err)
	}
}

func TestScheduleValidation(t *testing.T) {
	f := newFixture(t)
	for _, bad := range []obj{
		{"at": "08:00"},            // no prompt
		{"prompt": "x"},            // no time
		{"prompt": "x", "at": "8"}, // not a time
		{"prompt": "x", "at": "08:00", "days": "funday"},
		{"prompt": "x", "at": "08:00", "profile": "nope"},
		{"prompt": "x", "at": "08:00", "cwd": "/no/such/dir"},
	} {
		f.wantError(f.post("/api/schedules", bad), 400)
	}
	sc := f.ok(f.post("/api/schedules", obj{"prompt": "Summarise the commits\nand post them", "at": "8:00", "days": "fri,mon"}))
	eq(t, []any{sc.str("title"), sc.str("at"), sc.str("days"), sc.str("profile"), sc.data["catch_up"], sc.data["enabled"]},
		[]any{"Summarise the commits", "08:00", "mon,fri", "team", false, true})
	if sc.data["next"].(float64) == 0 {
		t.Error("an enabled schedule has no next run")
	}
	off := f.ok(f.patch("/api/schedules/"+sc.str("id")[:6], obj{"enabled": false, "catch_up": true}))
	eq(t, []any{off.data["enabled"], off.data["catch_up"], off.data["next"]}, []any{false, true, float64(0)})
	f.wantError(f.patch("/api/schedules/"+sc.str("id"), obj{"at": "25:00"}), 400)
	f.wantError(f.patch("/api/schedules/zzzz", obj{"enabled": true}), 404)
	eq(t, f.ok(f.do("DELETE", "/api/schedules/"+sc.str("id"), nil)).str("deleted"), sc.str("id"))
	eq(t, f.count("schedules"), 0)
}

func TestSchedulesRun(t *testing.T) {
	f := newFixture(t)
	f.cfg.TmuxSession = "main"
	standup := f.ok(f.post("/api/schedules", obj{"title": "Standup", "prompt": "summarise the commits", "at": "08:00",
		"cwd": f.tmp, "profile": "personal", "catch_up": true})).str("id")
	report := f.ok(f.post("/api/schedules", obj{"title": "Report", "prompt": "fetch the status", "at": "09:00"})).str("id")
	if _, err := f.st.Exec("UPDATE schedules SET created_at = ?", float64(monday(-7, 0, 0).Unix())); err != nil {
		t.Fatal(err)
	}

	// 08:00 on Monday: the standup starts, the report is not due yet.
	f.runDue(monday(0, 8, 0))
	sc := f.schedule(standup)
	task, _ := f.st.Task(sc.LastTask)
	if task == nil {
		t.Fatalf("no task started: %+v", sc)
	}
	eq(t, []string{task.Title, task.Prompt, task.Profile, task.Source, task.State, task.Cwd, task.Schedule},
		[]string{"Standup · Mon Jan 7", "summarise the commits", "personal", "schedule", "running", f.tmp, standup})
	eq(t, f.history(task.ID), []string{"backlog schedule", "running schedule"})
	eq(t, len(f.tmux.windows), 1)

	// The next tick runs nothing twice.
	f.runDue(monday(0, 8, 0).Add(30 * time.Second))
	eq(t, f.count("tasks"), 1)

	// Asleep from 08:30 to 11:00: the report has no catch-up, so 09:00 is skipped.
	f.runDue(monday(0, 11, 0))
	eq(t, f.count("tasks"), 1)
	eq(t, f.schedule(report).LastRun, float64(0))

	// Tuesday the machine wakes at 10:30: the standup catches up, the report waits for Wednesday.
	f.runDue(monday(1, 10, 30))
	eq(t, f.count("tasks"), 2)
	eq(t, f.schedule(report).LastRun, float64(0))
	tuesday, _ := f.st.Task(f.schedule(standup).LastTask)
	eq(t, tuesday.Title, "Standup · Tue Jan 8")

	// Wednesday the machine is up from 07:00: each runs at its time, the standup no later than 08:00.
	f.runDue(monday(2, 7, 0))
	eq(t, f.count("tasks"), 2)
	f.runDue(monday(2, 8, 0))
	f.runDue(monday(2, 9, 0))
	eq(t, f.count("tasks"), 4)

	// Run now: starts a task whatever the time, and says how it is going.
	run := f.ok(f.post("/api/schedules/"+report+"/run", nil))
	eq(t, run.str("last_state"), "Running")
	eq(t, f.count("tasks"), 5)
}

func TestScheduleThatCannotStart(t *testing.T) {
	f := newFixture(t)
	id := f.ok(f.post("/api/schedules", obj{"prompt": "x", "at": "08:00", "catch_up": true})).str("id")
	if _, err := f.st.Exec("UPDATE schedules SET created_at = 0"); err != nil {
		t.Fatal(err)
	}
	f.tmux.available = false
	f.runDue(monday(0, 9, 0))
	sc := f.schedule(id)
	if sc.LastError == "" || sc.LastRun == 0 {
		t.Errorf("the failure is not recorded: %+v", sc)
	}
	// It is not retried every tick.
	f.runDue(monday(0, 9, 1))
	eq(t, f.count("tasks"), 0)
	f.wantError(f.post("/api/schedules/"+id+"/run", nil), 503)
}

func TestScheduleRetiresItsPreviousRun(t *testing.T) {
	f := newFixture(t)
	f.cfg.TmuxSession = "main"
	id := f.ok(f.post("/api/schedules", obj{"title": "Standup", "prompt": "summarise the commits", "at": "08:00"})).str("id")
	if _, err := f.st.Exec("UPDATE schedules SET created_at = ?", float64(monday(-7, 0, 0).Unix())); err != nil {
		t.Fatal(err)
	}
	set := func(task, state, by string) {
		t.Helper()
		if _, err := f.st.SetState(task, state, by, nil); err != nil {
			t.Fatal(err)
		}
	}
	run := func(days int) string {
		t.Helper()
		f.runDue(monday(days, 8, 0))
		return f.schedule(id).LastTask
	}

	// A run left in Review is replaced by the next one.
	first := run(0)
	eq(t, f.ok(f.get("/api/tasks/" + first)).data["tags"], []any{"team", "schedule"})
	set(first, "review", "hook")
	run(1)
	eq(t, f.task(first).State, "done")
	eq(t, f.task(first).Last, "Replaced by the next run")
	eq(t, f.task(first).Hidden, true)

	// A run the board thought stuck while it opened, then left in Review, is replaced too.
	slow := f.schedule(id).LastTask
	set(slow, "needs", "board")
	set(slow, "running", "hook")
	set(slow, "review", "hook")
	run(2)
	eq(t, f.task(slow).State, "done")

	// A run you replied to went back to work after its Review: it stays.
	replied := f.schedule(id).LastTask
	set(replied, "review", "hook")
	set(replied, "running", "hook")
	set(replied, "review", "hook")
	run(3)
	eq(t, f.task(replied).State, "review")

	// So does one whose first turn ended in Done.
	again := f.schedule(id).LastTask
	set(again, "done", "hook")
	set(again, "running", "hook")
	set(again, "review", "hook")
	run(4)
	eq(t, f.task(again).State, "review")

	// A run you moved stays.
	moved := f.schedule(id).LastTask
	f.ok(f.patch("/api/tasks/"+moved, obj{"state": "review"}))
	run(5)
	eq(t, f.task(moved).State, "review")

	// A run that waited on you, and you answered, stays.
	answered := f.schedule(id).LastTask
	set(answered, "needs", "hook")
	set(answered, "running", "hook")
	set(answered, "review", "hook")
	run(6)
	eq(t, f.task(answered).State, "review")

	// A run still working stays.
	working := f.schedule(id).LastTask
	run(7)
	eq(t, f.task(working).State, "running")

	// A run that cannot start leaves the previous one where it is.
	last := f.schedule(id).LastTask
	set(last, "review", "hook")
	f.tmux.available = false
	run(8)
	eq(t, f.task(last).State, "review")
	eq(t, f.schedule(id).LastTask, last)

	// A run created but whose window fails to open leaves it too.
	f.tmux.available, f.tmux.fail = true, "boom"
	run(9)
	eq(t, f.task(last).State, "review")

	// Once a run starts again, the last run that started is retired.
	eq(t, f.schedule(id).LastTask, last)
	f.tmux.fail = ""
	run(10)
	eq(t, f.task(last).State, "done")
}

func TestCronSchedule(t *testing.T) {
	f := newFixture(t)
	f.cfg.TmuxSession = "main"
	f.wantError(f.post("/api/schedules", obj{"prompt": "x", "at": "*/15 * * *"}), 400)
	f.wantError(f.post("/api/schedules", obj{"prompt": "x", "at": "0 9 * * *", "days": "weekdays"}), 400)
	id := f.ok(f.post("/api/schedules", obj{"title": "Triage", "prompt": "sort new issues", "at": "*/15  * * * *"})).str("id")
	eq(t, f.schedule(id).At, "*/15 * * * *")
	if _, err := f.st.Exec("UPDATE schedules SET created_at = ?", float64(monday(-1, 0, 0).Unix())); err != nil {
		t.Fatal(err)
	}

	// Its title carries the time, as it runs several times a day.
	f.runDue(monday(0, 9, 0))
	first := f.schedule(id).LastTask
	task, _ := f.st.Task(first)
	eq(t, task.Title, "Triage · Mon Jan 7 09:00")

	// 09:15: the 09:00 run still works, so this one waits. Without catch-up, past the grace it is
	// skipped, and LAST RUN still says 09:00.
	f.runDue(monday(0, 9, 15))
	f.runDue(monday(0, 9, 21))
	eq(t, f.count("tasks"), 1)
	eq(t, f.schedule(id).LastTask, first)
	eq(t, f.schedule(id).LastRun, float64(monday(0, 9, 0).Unix()))

	// 09:30: the 09:00 run is in Review, so the next one starts and retires it.
	if _, err := f.st.SetState(first, "review", "hook", nil); err != nil {
		t.Fatal(err)
	}
	f.runDue(monday(0, 9, 30))
	eq(t, f.count("tasks"), 2)
	done, _ := f.st.Task(first)
	eq(t, done.State, "done")

	// With catch-up, a run that waited starts as soon as the previous one is done.
	f.ok(f.patch("/api/schedules/"+id, obj{"catch_up": true}))
	f.runDue(monday(0, 9, 45))
	eq(t, f.count("tasks"), 2)
	if _, err := f.st.SetState(f.schedule(id).LastTask, "review", "hook", nil); err != nil {
		t.Fatal(err)
	}
	f.runDue(monday(0, 9, 52))
	eq(t, f.count("tasks"), 3)
}

func TestScheduleRunThatCannotOpenLeavesNoTask(t *testing.T) {
	f := newFixture(t)
	id := f.ok(f.post("/api/schedules", obj{"prompt": "x", "at": "* * * * *", "catch_up": true})).str("id")
	if _, err := f.st.Exec("UPDATE schedules SET created_at = 0"); err != nil {
		t.Fatal(err)
	}
	f.tmux.fail = "no window"
	f.runDue(monday(0, 9, 0))
	f.runDue(monday(0, 9, 1))
	if f.schedule(id).LastError == "" {
		t.Error("the failure is not recorded")
	}
	eq(t, f.count("tasks"), 0)
}
