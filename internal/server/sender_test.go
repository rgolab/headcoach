package server

import (
	"testing"

	"github.com/rouxlabs/headcoach/internal/transcript"
)

// sent lists who sent each message the board typed into a task, and what it typed.
func (f *fixture) sent(id string) []string {
	f.t.Helper()
	messages, err := f.st.Messages(id)
	if err != nil {
		f.t.Fatal(err)
	}
	var out []string
	for _, m := range messages {
		out = append(out, m.Sender+" "+m.SenderTask+" "+m.Kind+" "+m.Text)
	}
	return out
}

// A message another task sends carries a label, so the session never reads it as its user's words.
func TestReplyNamesItsSender(t *testing.T) {
	f := newFixture(t)
	child := f.live()
	f.tmux.known["%2"] = true
	parent := f.hook("UserPromptSubmit", "s2", obj{"prompt": "coordinate"}, "X-Tmux-Pane", "%2")
	path := "/api/tasks/" + child + "/reply"

	// From the parent's session, the way hc reply sends it from a Bash tool call.
	out := f.ok(f.do("POST", path, obj{"text": "stop\nfor now"}, "X-HC-Session", "s2", "X-HC-Agent", "1"))
	eq(t, f.tmux.sent, []string{"%1|[from task s2] stop\nfor now|"})
	eq(t, out.str("last"), "From task s2: stop")
	// From a session the board started, known by the task id it put in the environment.
	f.ok(f.do("POST", path, obj{"text": "go"}, "X-HC-Task", parent, "X-HC-Agent", "1"))
	// An agent the board cannot place is still not you, whatever it claims.
	f.ok(f.do("POST", path, obj{"text": "hi"}, "X-HC-Agent", "1"))
	f.ok(f.do("POST", path, obj{"text": "hi"}, "X-HC-Session", "unknown", "X-HC-Task", "gone"))
	// You, on a board or at a terminal outside any session.
	out = f.ok(f.post(path, obj{"text": "carry on"}))
	eq(t, out.str("last"), "You: carry on")
	eq(t, f.tmux.sent[1:], []string{
		"%1|[from task s2] go|", "%1|[from an agent] hi|", "%1|[from an agent] hi|", "%1|carry on|",
	})
	// A slash command keeps its place.
	f.ok(f.do("POST", path, obj{"text": "/reviewer the branch"}, "X-HC-Session", "s2"))
	eq(t, f.tmux.sent[5], "%1|/reviewer [from task s2] the branch|")

	// The answer to a question dialog carries it too, on its one line.
	f.hook("PreToolUse", "s1", obj{"tool_name": "AskUserQuestion", "tool_input": obj{"questions": []any{obj{"question": "A or B?"}}}})
	f.tmux.screen = "A or B?\n❯ 1. A\n  2. B\n  3. Type something.\n───\n  4. Chat about this\n"
	f.ok(f.do("POST", path, obj{"text": "both"}, "X-HC-Session", "s2"))
	eq(t, f.tmux.typed, []string{"%1|[from task s2] both"})
	// Keys carry no label: the board records who pressed them.
	f.ok(f.do("POST", "/api/tasks/"+child+"/keys", obj{"keys": []any{"Down", "Enter"}}, "X-HC-Session", "s2"))

	eq(t, f.sent(child), []string{
		"task s2 message [from task s2] stop\nfor now",
		"task s2 message [from task s2] go",
		"agent  message [from an agent] hi",
		"agent  message [from an agent] hi",
		"you  message carry on",
		"task s2 message /reviewer [from task s2] the branch",
		"task s2 answer [from task s2] both",
		"task s2 keys Down Enter",
	})

	// With no terminal left, the message resumes the session as its next prompt, label and all.
	f.hook("Stop", "s1", obj{"last_assistant_message": "done"})
	f.hook("SessionEnd", "s1", nil)
	delete(f.tmux.known, "%1")
	f.ok(f.do("POST", path, obj{"text": "next"}, "X-HC-Session", "s2"))
	eq(t, shellWords(t, f.tmux.windows[0].Command), []string{"claude-personal", "--resume", "s1", "--", "[from task s2] next"})
}

// The board's last line for a task tells a prompt another task sent from yours.
func TestPromptOfAnotherTaskShowsItsSender(t *testing.T) {
	f := newFixture(t)
	id := f.hook("UserPromptSubmit", "s1", obj{"prompt": "[from coordinator 1a2b3c4d] build it"})
	eq(t, f.task(id).Last, "From coordinator 1a2b3c4d: build it")
}

// A task another agent adds starts on a labeled prompt; its title is the prompt's own words.
func TestNewTaskFromAnotherTask(t *testing.T) {
	f := newFixture(t)
	f.live()
	out := f.ok(f.do("POST", "/api/tasks", obj{"prompt": "fix the login\nand test it", "cwd": f.tmp}, "X-HC-Session", "s1"))
	id := out.str("id")
	eq(t, []any{out.str("title"), out.str("prompt")}, []any{"fix the login", "[from task s1] fix the login\nand test it"})
	eq(t, f.sent(id), []string{"task s1 message [from task s1] fix the login\nand test it"})
	// Yours stays as you typed it.
	out = f.newTask(obj{"prompt": "plain"})
	eq(t, out.str("prompt"), "plain")
	eq(t, len(f.sent(out.str("id"))), 0)
}

// The log names who sent each message the board typed; a label typed by hand stays yours.
func TestLogNamesTheSender(t *testing.T) {
	f := newFixture(t)
	id := f.live()
	f.tmux.known["%2"] = true
	f.hook("UserPromptSubmit", "s2", obj{"prompt": "coordinate"}, "X-Tmux-Pane", "%2")
	f.ok(f.do("POST", "/api/tasks/"+id+"/reply", obj{"text": "stop   here"}, "X-HC-Session", "s2"))
	f.srv.Log = func(paths []string, limit int) []transcript.Turn {
		return []transcript.Turn{
			{Who: "you", Text: "hello"},
			{Who: "you", Text: "[from task s2] stop here"},
			{Who: "you", Text: "[from task s2] I typed this myself"},
			{Who: "agent", Text: "[from task s2] stop here"},
		}
	}
	eq(t, f.get("/api/tasks/" + id).data["log"], []any{
		obj{"who": "you", "text": "hello"},
		obj{"who": "you", "text": "[from task s2] stop here", "from": "task s2"},
		obj{"who": "you", "text": "[from task s2] I typed this myself"},
		obj{"who": "agent", "text": "[from task s2] stop here", "html": "<p>[from task s2] stop here</p>\n"},
	})
}
