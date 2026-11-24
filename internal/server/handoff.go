package server

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/humblebundledore/headcoach/internal/api"
	"github.com/humblebundledore/headcoach/internal/board"
	"github.com/humblebundledore/headcoach/internal/handoff"
	"github.com/humblebundledore/headcoach/internal/store"
	"github.com/humblebundledore/headcoach/internal/transcript"
)

// handoffRead is as much of a handoff file as the board reads: a handoff is a page, not a log.
const handoffRead = 64 << 10

// readHandoff reads a task's handoff file. Its path is there even when the file is not.
func (s *Server) readHandoff(taskID string) *api.Handoff {
	out := &api.Handoff{Path: s.cfg.HandoffPath(taskID), Summary: handoff.Parse("")}
	fh, err := os.Open(out.Path)
	if err != nil {
		return out
	}
	defer fh.Close()
	info, err := fh.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return out
	}
	raw, err := io.ReadAll(io.LimitReader(fh, handoffRead))
	if err != nil {
		return out
	}
	out.Text, out.UpdatedAt = string(raw), float64(info.ModTime().UnixNano())/1e9
	out.HTML, out.Summary = transcript.Render(out.Text), handoff.Parse(out.Text)
	return out
}

// written tells whether a handoff says anything: a template nobody filled in does not.
func written(h *api.Handoff) bool {
	sum := h.Summary
	return sum.Status != "" || len(sum.Running)+len(sum.Waiting)+len(sum.Next)+sum.Queue.Open+sum.Queue.Done > 0
}

func (s *Server) getHandoff(r *http.Request) (any, error) {
	task, err := s.find(r)
	if err != nil {
		return nil, err
	}
	return s.readHandoff(task.ID), nil
}

// initHandoff writes the template where a task's handoff goes, unless a handoff is there already.
func (s *Server) initHandoff(r *http.Request) (any, error) {
	task, err := s.find(r)
	if err != nil {
		return nil, err
	}
	path := s.cfg.HandoffPath(task.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	fh, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err == nil {
		_, err = fh.WriteString(handoff.Template)
		if closeErr := fh.Close(); err == nil {
			err = closeErr
		}
	}
	if err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, err
	}
	return s.readHandoff(task.ID), nil
}

// reset clears a task's context and points the fresh one at its handoff: /clear, then a brief. The
// task must be between two turns, with a handoff that says something, so nothing is lost on the way.
func (s *Server) reset(r *http.Request) (any, error) {
	s.mu.Lock()
	task, sess, path, err := s.resetInput(r)
	if err == nil {
		var from sender
		if from, err = s.caller(r); err == nil {
			if err = s.tmux.SendText(sess.Pane, "/clear", sess.Socket); err != nil {
				err = fail(502, "tmux could not clear the session: %v", err)
			} else {
				err = s.record(task, from, "reset", "/clear")
			}
		}
	}
	since := store.Now()
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	// The cleared session starts again under a new id, in the same pane: wait for it and its prompt.
	for deadline := time.Now().Add(s.clearWait); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		s.mu.Lock()
		fresh, err := s.st.Sessions("WHERE task_id = ? AND started_at >= ?", task.ID, since)
		typed, shown := promptBox(s.tmux.CaptureStyled(sess.Pane, sess.Socket))
		s.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if len(fresh) > 0 && shown && typed == "" {
			break
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	brief := board.Label(board.FromHC, "", false) + "Your context was cleared. Read your handoff file, " + path +
		", then go on from where it says."
	if err := s.tmux.SendText(sess.Pane, brief, sess.Socket); err != nil {
		return nil, fail(502, "the session was cleared, but tmux could not brief it: %v", err)
	}
	if err := s.record(task, sender{kind: board.FromHC}, "message", brief); err != nil {
		return nil, err
	}
	if err := s.st.UpdateTask(task.ID, store.Fields{"last": board.Said(brief)}); err != nil {
		return nil, err
	}
	return s.refreshed(task.ID)
}

// resetInput checks that a task can be reset now, and returns its session and its handoff's path.
func (s *Server) resetInput(r *http.Request) (*store.Task, *store.Session, string, error) {
	task, err := s.find(r)
	if err != nil {
		return nil, nil, "", err
	}
	h := s.readHandoff(task.ID)
	if !written(h) {
		return nil, nil, "", fail(409, "Nothing to start again from: write the task's handoff first, at %s. hc handoff init gives a template.", h.Path)
	}
	between := task.State == "done" || task.State == "review" || task.State == "needs" && task.AskKind == "question"
	if !between {
		return nil, nil, "", fail(409, "This task is %s: reset it between two turns", store.StateName(task.State))
	}
	sess, err := s.liveSession(task.ID)
	if err != nil {
		return nil, nil, "", err
	}
	if sess == nil {
		return nil, nil, "", fail(409, "This task has no live session: start it, its handoff is waiting at %s", h.Path)
	}
	if typed, shown := promptBox(s.tmux.CaptureStyled(sess.Pane, sess.Socket)); !shown || typed != "" {
		return nil, nil, "", fail(409, "The session's prompt is not empty, or a dialog is up: nothing was cleared")
	}
	return task, sess, h.Path, nil
}
