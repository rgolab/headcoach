package server

import (
	"testing"
	"time"

	"github.com/humblebundledore/headcoach/internal/store"
)

func TestTimerIsSetClearedAndChecked(t *testing.T) {
	f := newFixture(t)
	out := f.newTask(obj{"timer": 600})
	id := out.str("id")
	eq(t, out.data["timer_left"], float64(600))
	if ends := f.task(id).EndsAt; ends < store.Now()+590 || ends > store.Now()+600 {
		t.Errorf("ends_at %v is not ten minutes from now", ends)
	}
	eq(t, f.newTask(nil).data["timer_left"], float64(0))

	path := "/api/tasks/" + id
	eq(t, f.ok(f.patch(path, obj{"timer": 90})).data["timer_left"], float64(90))
	eq(t, f.ok(f.patch(path, obj{"timer": 0})).data["timer_left"], float64(0))
	eq(t, f.task(id).EndsAt, float64(0))
	for _, bad := range []any{-1, 1.5, "10m", nil, true, maxTimer + 1} {
		f.wantError(f.patch(path, obj{"timer": bad}), 400)
		f.wantError(f.post("/api/tasks", obj{"prompt": "x", "cwd": f.tmp, "timer": bad}), 400)
	}
	eq(t, f.count("tasks"), 2)
}

func TestExpireClosesWhateverTheTaskDoes(t *testing.T) {
	f := newFixture(t)
	running := f.live()
	f.ok(f.patch("/api/tasks/"+running, obj{"timer": 60}))
	later := f.newTask(obj{"timer": 3600}).str("id")
	// A task you moved to Done, its session still open in pane %2.
	f.tmux.known["%2"] = true
	done := f.hook("UserPromptSubmit", "s2", obj{"prompt": "keep me"}, "X-Tmux-Pane", "%2")
	f.ok(f.patch("/api/tasks/"+done, obj{"timer": 60}))
	f.ok(f.patch("/api/tasks/"+done, obj{"state": "done"}))

	// Not yet: nothing moves.
	if err := f.srv.Expire(time.Now()); err != nil {
		t.Fatal(err)
	}
	eq(t, f.task(running).State, "running")

	if err := f.srv.Expire(time.Now().Add(2 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	task := f.task(running)
	eq(t, []any{task.State, task.EndsAt, task.Last}, []any{"done", float64(0), "Closed when its timer ran out"})
	eq(t, f.tmux.killed, []string{"%1|"})
	eq(t, f.session("s1").Live, false)
	eq(t, f.history(running), []string{"running hook", "done timer"})
	// A task already in Done only loses its timer, its session left open; one with time left keeps it.
	eq(t, []any{f.task(done).State, f.task(done).EndsAt, f.session("s2").Live}, []any{"done", float64(0), true})
	eq(t, f.history(done)[len(f.history(done))-1], "done you")
	eq(t, f.task(later).State, "backlog")
	if f.task(later).EndsAt == 0 {
		t.Error("the task with time left lost its timer")
	}

	// The session's own end, once its pane is gone, leaves the task in Done.
	f.hook("SessionEnd", "s1", obj{"reason": "other"})
	eq(t, f.task(running).State, "done")
}

func TestTheScheduleLoopClosesExpiredTasks(t *testing.T) {
	f := newFixture(t)
	id := f.newTask(obj{"timer": 1}).str("id")
	if err := f.st.UpdateTask(id, store.Fields{"ends_at": store.Now() - 1}); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	close(stop)
	f.srv.RunSchedules(stop) // one pass, then it sees stop
	eq(t, f.task(id).State, "done")
}

func TestAScheduleTimesEachRun(t *testing.T) {
	f := newFixture(t)
	sc := f.ok(f.post("/api/schedules", obj{"prompt": "triage", "at": "*/15 * * * *", "timer": 600}))
	id := sc.str("id")
	eq(t, sc.data["timer"], float64(600))
	f.wantError(f.patch("/api/schedules/"+id, obj{"timer": -5}), 400)
	f.wantError(f.post("/api/schedules", obj{"prompt": "x", "at": "08:00", "timer": "10m"}), 400)

	f.runDue(monday(0, 9, 0))
	run := f.task(f.schedule(id).LastTask)
	if left := run.EndsAt - store.Now(); left < 590 || left > 600 {
		t.Errorf("the run's timer has %v seconds left", left)
	}
	eq(t, f.ok(f.patch("/api/schedules/"+id, obj{"timer": 0})).data["timer"], float64(0))
	f.ok(f.patch("/api/tasks/"+run.ID, obj{"state": "done"}))
	f.runDue(monday(0, 9, 15))
	eq(t, f.task(f.schedule(id).LastTask).EndsAt, float64(0))
}
