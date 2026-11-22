package server

import (
	"testing"
)

func TestATaskStartsOnItsModelAndEffort(t *testing.T) {
	f := newFixture(t)
	task := f.newTask(obj{"model": " sonnet ", "effort": "LOW", "start": true})
	id := task.str("id")
	eq(t, []any{task.data["model"], task.data["effort"]}, []any{"sonnet", "low"})
	eq(t, shellWords(t, f.tmux.windows[0].Command),
		[]string{"claude-team", "--model", "sonnet", "--effort", "low", "--session-id", id, "--", "do the thing"})
	eq(t, task.data["resume"], "claude-team --model sonnet --effort low --session-id "+id)

	// Left out, the profile's own: no option at all.
	plain := f.newTask(obj{"start": true})
	eq(t, shellWords(t, f.tmux.windows[1].Command), []string{"claude-team", "--session-id", plain.str("id"), "--", "do the thing"})

	f.newTask(obj{"model": "opus[1m]", "start": true})
	eq(t, shellWords(t, f.tmux.windows[2].Command)[1:3], []string{"--model", "opus[1m]"})

	// default is the profile's too, never a model of that name.
	fallback := f.newTask(obj{"model": "Default", "effort": "default", "start": true})
	eq(t, []any{fallback.data["model"], fallback.data["effort"]}, []any{"", ""})
	eq(t, shellWords(t, f.tmux.windows[3].Command)[1], "--session-id")

	for _, bad := range []obj{{"model": "--dangerously-skip-permissions"}, {"model": "son net"}, {"model": "x;id"},
		{"model": "$(id)"}, {"effort": "huge"}, {"effort": "low --x"}} {
		bad["prompt"], bad["cwd"] = "x", f.tmp
		f.wantError(f.post("/api/tasks", bad), 400)
	}
	eq(t, f.count("tasks"), 4)
}

func TestAScheduleStartsEachRunOnItsModelAndEffort(t *testing.T) {
	f := newFixture(t)
	sc := f.ok(f.post("/api/schedules", obj{"prompt": "triage", "at": "*/15 * * * *", "model": "haiku", "effort": "low"}))
	id := sc.str("id")
	eq(t, []any{sc.data["model"], sc.data["effort"]}, []any{"haiku", "low"})
	f.wantError(f.patch("/api/schedules/"+id, obj{"effort": "extreme"}), 400)
	f.wantError(f.patch("/api/schedules/"+id, obj{"model": "-x"}), 400)
	f.wantError(f.post("/api/schedules", obj{"prompt": "x", "at": "08:00", "model": "a b"}), 400)

	f.runDue(monday(0, 9, 0))
	run := f.task(f.schedule(id).LastTask)
	eq(t, []string{run.Model, run.Effort}, []string{"haiku", "low"})
	eq(t, shellWords(t, f.tmux.windows[0].Command)[1:5], []string{"--model", "haiku", "--effort", "low"})

	// Changed, then back to the profile's: the next run follows.
	eq(t, f.ok(f.patch("/api/schedules/"+id, obj{"model": "sonnet"})).data["effort"], "low")
	patched := f.ok(f.patch("/api/schedules/"+id, obj{"effort": "default"}))
	eq(t, []any{patched.data["model"], patched.data["effort"]}, []any{"sonnet", ""})
	f.ok(f.patch("/api/tasks/"+run.ID, obj{"state": "done"}))
	f.runDue(monday(0, 9, 15))
	next := f.task(f.schedule(id).LastTask)
	eq(t, []string{next.Model, next.Effort}, []string{"sonnet", ""})
}
