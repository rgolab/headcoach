package server

import (
	"math"
	"time"

	"github.com/humblebundledore/headcoach/internal/store"
)

// maxTimer is the longest timer a task takes: a week is already longer than any run it is meant for.
const maxTimer = 7 * 24 * 3600

// timerField reads the timer of a request, in whole seconds. Zero means no timer.
func timerField(data map[string]any) (int, error) {
	value, ok := data["timer"].(float64)
	if !ok || value < 0 || value > maxTimer || value != math.Trunc(value) {
		return 0, fail(400, "The timer is a whole number of seconds, from 0 for none to %d", maxTimer)
	}
	return int(value), nil
}

// endsAt is when a timer set now runs out. Zero for no timer.
func endsAt(timer int) float64 {
	if timer == 0 {
		return 0
	}
	return store.Now() + float64(timer)
}

// timerLeft is how many seconds a task's timer has left, at least 1 while it runs. Zero without a timer.
func timerLeft(task *store.Task, now float64) int {
	if task.EndsAt == 0 {
		return 0
	}
	return max(1, int(math.Ceil(task.EndsAt-now)))
}

// Expire closes the tasks whose timer ran out by now: their session ends, as when parked, and they
// move to Done, whatever they were doing. The timer is a hard limit, so a run that hangs never lingers.
func (s *Server) Expire(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	due, err := s.st.Tasks("WHERE ends_at > 0 AND ends_at <= ?", float64(now.UnixNano())/1e9)
	if err != nil {
		return err
	}
	for i := range due {
		task := &due[i]
		// A task you moved to Done yourself is closed already: a session you kept open there stays.
		if task.State == "done" {
			if err := s.st.UpdateTask(task.ID, store.Fields{"ends_at": 0}); err != nil {
				return err
			}
			continue
		}
		live, err := s.liveSession(task.ID)
		if err != nil {
			return err
		}
		if live != nil {
			s.tmux.KillPane(live.Pane, live.Socket)
		}
		// Done first: a running task whose sessions close would otherwise go to Review on the way.
		_, err = s.st.SetState(task.ID, "done", "timer", store.Fields{"ends_at": 0, "last": "Closed when its timer ran out"})
		if err != nil {
			return err
		}
		if err := s.closeSessions(task.ID); err != nil {
			return err
		}
	}
	return nil
}
