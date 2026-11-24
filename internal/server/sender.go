package server

import (
	"net/http"
	"strings"

	"github.com/humblebundledore/headcoach/internal/board"
	"github.com/humblebundledore/headcoach/internal/store"
	"github.com/humblebundledore/headcoach/internal/transcript"
)

// sender is who sent a request: you, a task, or an agent the board cannot tie to a task.
type sender struct {
	kind string // board.FromYou, board.FromTask or board.FromAgent
	task string // the sending task's id, for board.FromTask
}

// caller tells who sent a request, from what the client says about where it runs. A command an
// agent runs carries its session, so its words never pass for yours; the boards send nothing.
// Anything on the machine can reach the daemon: this keeps honest agents apart, not hostile ones.
func (s *Server) caller(r *http.Request) (sender, error) {
	sid, hint := r.Header.Get("X-HC-Session"), r.Header.Get("X-HC-Task")
	if sid != "" {
		sess, err := s.st.Session(sid)
		if err != nil {
			return sender{}, err
		}
		if sess != nil && sess.TaskID != "" {
			return sender{board.FromTask, sess.TaskID}, nil
		}
	}
	if hint != "" {
		task, err := s.st.Task(hint)
		if err != nil {
			return sender{}, err
		}
		if task != nil {
			return sender{board.FromTask, task.ID}, nil
		}
	}
	// A session the board does not know yet is still not you.
	if sid != "" || hint != "" || r.Header.Get("X-HC-Agent") != "" {
		return sender{kind: board.FromAgent}, nil
	}
	return sender{kind: board.FromYou}, nil
}

// label is what goes before a message from this sender to a task.
func (from sender) label(to *store.Task) string {
	return board.Label(from.kind, from.task, false)
}

// record keeps a message the board typed into a task's session, with who sent it.
func (s *Server) record(task *store.Task, from sender, kind, text string) error {
	return s.st.AddMessage(store.Message{TaskID: task.ID, Sender: from.kind, SenderTask: from.task, Kind: kind, Text: text})
}

// squeezed is a text on one line, its whitespace squeezed: how a log turn and the message it shows compare.
func squeezed(text string) string { return strings.Join(strings.Fields(text), " ") }

// senders names who sent each turn of a log that the board typed for another agent. A turn whose label
// matches no message the board delivered stays yours: the label was typed by hand.
func senders(log []transcript.Turn, sent []store.Message) {
	for i := range log {
		turn := &log[i]
		if turn.Who != "you" {
			continue
		}
		from, _ := board.SplitLabel(turn.Text)
		if from == "" {
			continue
		}
		shown := squeezed(strings.TrimSuffix(turn.Text, "…"))
		for j := len(sent) - 1; j >= 0; j-- {
			if sent[j].Sender != board.FromYou && strings.HasPrefix(squeezed(sent[j].Text), shown) {
				turn.From = from
				break
			}
		}
	}
}
