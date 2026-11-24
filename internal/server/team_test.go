package server

import (
	"os"
	"testing"
)

// The coordinator view: the tasks under a task at any depth, and what waits on you among them.
func TestTeam(t *testing.T) {
	f := newFixture(t)
	boss := f.live()
	kid := f.newTask(obj{"title": "kid", "parent": boss}).str("id")
	grandkid := f.newTask(obj{"title": "grandkid", "parent": kid}).str("id")
	f.newTask(obj{"title": "elsewhere"})
	hidden := f.newTask(obj{"title": "hidden", "parent": boss}).str("id")
	f.newTask(obj{"title": "under hidden", "parent": hidden})
	if err := f.st.UpdateTask(hidden, map[string]any{"hidden": 1}); err != nil {
		t.Fatal(err)
	}
	f.ok(f.patch("/api/tasks/"+grandkid, obj{"state": "review"}))
	os.MkdirAll(f.tmp+"/state/handoff", 0o755)
	os.WriteFile(f.cfg.HandoffPath(kid), []byte(filled), 0o644)
	os.WriteFile(f.cfg.HandoffPath(boss), []byte("## Waiting\n- on you: the budget\n"), 0o644)

	team := f.ok(f.get("/api/tasks/" + boss[:1] + "/team")).data
	eq(t, team["task"].(map[string]any)["id"], boss)
	var members [][]any
	for _, m := range team["members"].([]any) {
		m := m.(map[string]any)
		members = append(members, []any{m["title"], m["depth"], m["handoff"].(map[string]any)["status"], m["handoff_at"] != 0.0})
	}
	eq(t, members, [][]any{{"kid", 0.0, "Halfway.", true}, {"grandkid", 1.0, "", false}})
	var onYou [][]any
	for _, o := range team["on_you"].([]any) {
		o := o.(map[string]any)
		onYou = append(onYou, []any{o["title"], o["kind"], o["what"]})
	}
	eq(t, onYou, [][]any{{"hello", "handoff", "the budget"}, {"kid", "handoff", "a go"}, {"grandkid", "review", "Moved by you"}})

	// From inside a session, self is its own team; a task without children has an empty one.
	eq(t, f.ok(f.do("GET", "/api/tasks/self/team", nil, "X-HC-Session", "s1")).data["task"].(map[string]any)["id"], boss)
	eq(t, len(f.ok(f.get("/api/tasks/" + grandkid + "/team")).data["members"].([]any)), 0)
}
