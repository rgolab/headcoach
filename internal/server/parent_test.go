package server

import "testing"

// A task can be put under another one: when created, by link, or from the session of the parent itself.
func TestParent(t *testing.T) {
	f := newFixture(t)
	boss := f.live()
	kid := f.newTask(obj{"title": "kid", "parent": boss[:1]}).str("id")
	eq(t, f.task(kid).Parent, boss)
	// self is the task of the session the request comes from.
	other := f.ok(f.do("POST", "/api/tasks", obj{"title": "other", "parent": "self"}, "X-HC-Session", "s1")).str("id")
	eq(t, f.task(other).Parent, boss)
	f.wantError(f.newTask2(obj{"title": "x", "parent": "self"}), 400)
	f.wantError(f.newTask2(obj{"title": "x", "parent": "nope"}), 404)

	// Unlink, link again, and refuse a loop at any depth.
	f.ok(f.patch("/api/tasks/"+other, obj{"parent": ""}))
	eq(t, f.task(other).Parent, "")
	out := f.ok(f.patch("/api/tasks/"+other, obj{"parent": kid}))
	eq(t, out.str("parent"), kid)
	for _, loop := range []struct{ child, parent string }{{boss, boss}, {boss, kid}, {boss, other}, {kid, other}} {
		f.wantError(f.patch("/api/tasks/"+loop.child, obj{"parent": loop.parent}), 409)
	}
	// A refused request changes nothing else.
	f.wantError(f.patch("/api/tasks/"+boss, obj{"parent": other, "title": "renamed"}), 409)
	eq(t, []any{f.task(boss).Parent, f.task(boss).Title}, []any{"", "hello"})

	// self works as an id too, from inside a session.
	eq(t, f.ok(f.do("GET", "/api/tasks/self", nil, "X-HC-Session", "s1")).str("id"), boss)
	f.wantError(f.get("/api/tasks/self"), 400)

	// Deleting a parent leaves its children at the top.
	f.ok(f.do("DELETE", "/api/tasks/"+kid, nil))
	eq(t, f.task(other).Parent, "")
}

// The parent's messages reach its child from its coordinator.
func TestCoordinatorLabel(t *testing.T) {
	f := newFixture(t)
	child := f.live()
	f.tmux.known["%2"] = true
	boss := f.hook("UserPromptSubmit", "s2", obj{"prompt": "coordinate"}, "X-Tmux-Pane", "%2")
	f.ok(f.patch("/api/tasks/"+child, obj{"parent": boss}))
	f.ok(f.do("POST", "/api/tasks/"+child+"/reply", obj{"text": "stop"}, "X-HC-Session", "s2"))
	eq(t, f.tmux.sent, []string{"%1|[from coordinator s2] stop|"})
	// The first prompt of a task it adds under itself, too.
	out := f.ok(f.do("POST", "/api/tasks", obj{"prompt": "build it", "parent": "self"}, "X-HC-Session", "s2"))
	eq(t, out.str("prompt"), "[from coordinator s2] build it")
	// Another task is only a task.
	f.ok(f.do("POST", "/api/tasks/"+boss+"/reply", obj{"text": "done"}, "X-HC-Session", "s1"))
	eq(t, f.tmux.sent[1], "%2|[from task s1] done|")
}
