package server

import (
	"strings"
	"testing"

	"github.com/rouxlabs/headcoach/internal/store"
)

const rule = "────────────────────────────────────────"

// idleScreen is Claude Code waiting at an empty prompt.
var idleScreen = "⏺ Done.\n\n" + rule + "\n❯ \n" + rule + "\n  ? for shortcuts\n"

func TestPromptBox(t *testing.T) {
	for _, c := range []struct {
		screen string
		typed  string
		shown  bool
	}{
		{idleScreen, "", true},
		{"⏺ Done.\n" + rule + "\n❯ half a\n  sentence\n" + rule + "\n", "half a sentence", true},
		{"⏺ Done.\n" + rule + "\n> \n" + rule + "\n", "", true},
		// A dialog takes the place of the prompt.
		{"Do you want to proceed?\n" + rule + "\n❯ 1. Yes\n  2. No\n" + rule + "\n", "", false},
		{"A or B?\n❯ 1. A\n  2. B\n  3. Type something.\n───\n  4. Chat about this\n", "", false},
		{"", "", false},
		{rule + "\n" + rule + "\n", "", false},
		// As tmux captures it with its styles: a no-break space after the sign, words typed plain, and a
		// suggestion or a placeholder dim.
		{"\x1b[38;5;244m" + rule + "\x1b[39m\n\x1b[39m❯\u00a0half typed\n" + rule + "\n", "half typed", true},
		{rule + "\n\x1b[39m❯\u00a0\x1b[2mcheck on the child task\x1b[0m\n" + rule + "\n", "", true},
		{rule + "\n❯\u00a0\x1b[38;5;2mgreen words\x1b[0m\n" + rule + "\n", "green words", true},
		{rule + "\n❯\u00a0\x1b[2;22mnot dim\x1b[0m\n" + rule + "\n", "not dim", true},
		{rule + "\n\x1b[39m❯\u00a0\n" + rule + "\n", "", true},
	} {
		typed, shown := promptBox(c.screen)
		if typed != c.typed || shown != c.shown {
			t.Errorf("promptBox(%q) = %q, %v, want %q, %v", c.screen, typed, shown, c.typed, c.shown)
		}
	}
}

// coordinated builds a parent in pane %2, done with its turn, and a child under it in pane %1.
func coordinated(t *testing.T) (*fixture, string, string) {
	f := newFixture(t)
	child := f.live()
	f.tmux.known["%2"] = true
	parent := f.hook("UserPromptSubmit", "s2", obj{"prompt": "coordinate"}, "X-Tmux-Pane", "%2")
	f.hook("Stop", "s2", obj{"last_assistant_message": "Briefed the child."}, "X-Tmux-Pane", "%2")
	f.ok(f.patch("/api/tasks/"+child, obj{"parent": parent}))
	f.tmux.screen = idleScreen
	return f, parent, child
}

func (f *fixture) deliver(after float64) {
	f.t.Helper()
	if err := f.srv.Deliver(store.Now() + after); err != nil {
		f.t.Fatal(err)
	}
}

func TestInboxDelivers(t *testing.T) {
	f, parent, child := coordinated(t)
	f.hook("Stop", "s1", obj{"last_assistant_message": "Shall I merge it?"})

	// A state that passes in a moment is no news: the item waits a little first.
	f.deliver(0)
	eq(t, len(f.tmux.sent), 0)
	f.deliver(inboxSettle)
	eq(t, len(f.tmux.sent), 1)
	want := "%2|[from hc] Updates from the tasks you drive, not from your user:\n" +
		"- s1 \"hello\" asks: Shall I merge it?\nhc show <task> tells more.|"
	eq(t, f.tmux.sent[0], want)
	eq(t, f.task(parent).Last, "From hc: Updates from the tasks you drive, not from your user:")
	eq(t, f.sent(parent), []string{"hc  inbox " + strings.Split(want, "|")[1]})

	// Once said, never again.
	f.deliver(inboxSettle + inboxGap)
	eq(t, len(f.tmux.sent), 1)
	inbox := f.ok(f.get("/api/tasks/" + parent + "/inbox")).data["items"].([]any)
	item := inbox[0].(map[string]any)
	eq(t, []any{len(inbox), item["child_short"], item["child_title"], item["news"], item["delivered_at"] != 0.0},
		[]any{1, "s1", "hello", "asks: Shall I merge it?", true})
	_ = child
}

func TestInboxWaitsUntilTheParentCanHear(t *testing.T) {
	f, parent, child := coordinated(t)
	f.ok(f.patch("/api/tasks/"+child, obj{"state": "review"}))

	// Mid-turn, waiting on you, or at a dialog: it waits.
	f.hook("UserPromptSubmit", "s2", obj{"prompt": "go on"}, "X-Tmux-Pane", "%2")
	f.deliver(inboxSettle)
	f.hook("Stop", "s2", obj{"last_assistant_message": "Which one, A or B?"}, "X-Tmux-Pane", "%2")
	f.deliver(inboxSettle)
	eq(t, len(f.tmux.sent), 0)

	// Its turn is over, but a dialog or half-typed words are on its screen.
	f.hook("Stop", "s2", obj{"last_assistant_message": "Done."}, "X-Tmux-Pane", "%2")
	f.tmux.screen = "Do you want to proceed?\n" + rule + "\n❯ 1. Yes\n" + rule + "\n"
	f.deliver(inboxSettle)
	f.tmux.screen = "\n" + rule + "\n❯\u00a0half a sente\n" + rule + "\n"
	f.deliver(inboxSettle)
	eq(t, len(f.tmux.sent), 0)
	// A suggestion Claude Code draws there is no one typing.
	f.tmux.screen = "\n" + rule + "\n\x1b[39m❯\u00a0\x1b[2mcheck on the child\x1b[0m\n" + rule + "\n"
	f.deliver(inboxSettle)
	eq(t, len(f.tmux.sent), 1)
	if !strings.Contains(f.tmux.sent[0], `- s1 "hello" is in Review: Moved by your user`) {
		t.Errorf("sent %q", f.tmux.sent[0])
	}

	// A parent that only waits on its own jobs hears it too, but not twice a minute.
	f.hook("UserPromptSubmit", "s2", obj{"prompt": "watch"}, "X-Tmux-Pane", "%2")
	jobs := []any{obj{"id": "b1", "type": "monitor", "status": "running", "description": "watch the child"}}
	f.hook("Stop", "s2", obj{"last_assistant_message": "Waiting.", "background_tasks": jobs}, "X-Tmux-Pane", "%2")
	eq(t, []any{f.task(parent).State, f.task(parent).Waiting}, []any{"running", true})
	f.tmux.screen = idleScreen
	f.ok(f.patch("/api/tasks/"+child, obj{"state": "done"}))
	f.deliver(inboxSettle)
	eq(t, len(f.tmux.sent), 1)
	f.deliver(inboxSettle + inboxGap)
	eq(t, len(f.tmux.sent), 2)

	// No live session: it waits for one, the board never opens one for it.
	f.ok(f.patch("/api/tasks/"+child, obj{"state": "review"}))
	delete(f.tmux.known, "%2")
	f.deliver(inboxSettle + 2*inboxGap)
	eq(t, []any{len(f.tmux.sent), len(f.tmux.windows)}, []any{2, 0})
}

func TestInboxDropsWhatNoLongerHolds(t *testing.T) {
	f, parent, child := coordinated(t)
	// Back at work before the parent heard: nothing to say.
	f.hook("Stop", "s1", obj{"last_assistant_message": "Shall I?"})
	f.hook("UserPromptSubmit", "s1", obj{"prompt": "yes"})
	f.deliver(inboxSettle)
	eq(t, len(f.tmux.sent), 0)
	// A newer state replaces the older one.
	f.ok(f.patch("/api/tasks/"+child, obj{"state": "review"}))
	f.ok(f.patch("/api/tasks/"+child, obj{"state": "done"}))
	// A move the parent makes itself is no news to it.
	f.ok(f.do("PATCH", "/api/tasks/"+child, obj{"state": "parked"}, "X-HC-Session", "s2"))
	f.deliver(inboxSettle)
	eq(t, len(f.tmux.sent), 0)
	eq(t, f.history(child)[len(f.history(child))-1], "parked task:"+parent)
	items, _ := f.st.Inbox("WHERE parent_id = ?", parent)
	eq(t, len(items), 0)

	// A child taken from under its parent says nothing to it any more.
	f.ok(f.patch("/api/tasks/"+child, obj{"state": "review"}))
	f.ok(f.patch("/api/tasks/"+child, obj{"parent": ""}))
	f.deliver(inboxSettle)
	eq(t, len(f.tmux.sent), 0)
}

// The history names the task that moved another one.
func TestHistoryNamesTheTaskThatMoved(t *testing.T) {
	f, _, child := coordinated(t)
	f.ok(f.do("PATCH", "/api/tasks/"+child, obj{"state": "done"}, "X-HC-Session", "s2"))
	history := f.get("/api/tasks/" + child).data["history"].([]any)
	eq(t, history[len(history)-1].(map[string]any)["by"], "task s2")
	eq(t, f.task(child).Last, "Moved by task s2")
}
