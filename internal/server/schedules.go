package server

import (
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/humblebundledore/headcoach/internal/api"
	"github.com/humblebundledore/headcoach/internal/board"
	"github.com/humblebundledore/headcoach/internal/config"
	"github.com/humblebundledore/headcoach/internal/schedule"
	"github.com/humblebundledore/headcoach/internal/store"
)

// scheduleEvery is how often the daemon looks for a schedule that is due.
const scheduleEvery = 30 * time.Second

// RunSchedules starts the tasks of due schedules, and closes the tasks whose timer ran out, until stop
// is closed: once right away, then every scheduleEvery. A machine that slept is seen within that
// interval, and the schedules it missed then run when they catch up.
func (s *Server) RunSchedules(stop <-chan struct{}) {
	tick := time.NewTicker(scheduleEvery)
	defer tick.Stop()
	for {
		// Timers ride on the same tick: a run whose timer ran out is closed before the next one starts.
		if err := s.Expire(time.Now()); err != nil {
			log.Printf("timers: %v", err)
		}
		if err := s.RunDue(time.Now()); err != nil {
			log.Printf("schedules: %v", err)
		}
		select {
		case <-stop:
			return
		case <-tick.C:
		}
	}
}

// RunDue starts a task for every schedule that is due at now.
func (s *Server) RunDue(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.st.Schedules()
	if err != nil {
		return err
	}
	for _, sc := range all {
		if !schedule.Due(sc, now) {
			continue
		}
		if s.busy(sc) {
			continue
		}
		if err := s.fire(sc, now); err != nil {
			return err
		}
	}
	return nil
}

// busy tells whether a cron schedule's last run still works or waits on you. Its next run then waits,
// and starts once that one is done if catch-up or the grace allows: a schedule every few minutes
// would otherwise stack runs behind a slow one.
func (s *Server) busy(sc store.Schedule) bool {
	if !schedule.IsCron(sc.At) || sc.LastTask == "" {
		return false
	}
	task, err := s.st.Task(sc.LastTask)
	return err == nil && task != nil && (task.State == "running" || task.State == "needs")
}

// fire starts one run of a schedule. A run that cannot start is recorded on the schedule and not
// retried: it would fail the same way every 30 seconds.
func (s *Server) fire(sc store.Schedule, now time.Time) error {
	title := sc.Title + " · " + now.Format("Mon Jan 2")
	if schedule.IsCron(sc.At) {
		title += now.Format(" 15:04")
	}
	task, err := s.addTask(title, sc.Prompt, sc.Cwd, sc.Profile, sc.Model, sc.Effort, "schedule", sc.ID, "schedule", true, sc.Timer)
	fields := store.Fields{"last_run": float64(now.Unix()), "last_error": ""}
	if err != nil {
		log.Printf("schedule %s: %v", sc.Title, err)
		fields["last_error"] = err.Error()
		// A run that could not open stays nowhere: a schedule every minute would fill the backlog.
		if task != nil {
			if err := s.st.DeleteTask(task.ID); err != nil {
				log.Printf("schedule %s: %v", sc.Title, err)
			}
			task = nil
		}
	}
	// Only a run that started replaces the previous one. Otherwise the previous one stays the
	// schedule's last, so the next start still retires it.
	if task != nil && err == nil {
		fields["last_task"] = task.ID
		if err := s.retire(sc.LastTask); err != nil {
			log.Printf("schedule %s: %v", sc.Title, err)
		}
	}
	return s.st.UpdateSchedule(sc.ID, fields)
}

// retire moves the previous run of a schedule to Done, hidden, so the board shows only its latest run.
// It leaves a run that is not in Review, or that you took over: you moved it, it waited on you
// (the board's own guess that a session is stuck does not count), or the session went back to
// work after its first turn ended, in Review or Done, which is what a reply or an attach does.
func (s *Server) retire(id string) error {
	if id == "" {
		return nil
	}
	task, err := s.st.Task(id)
	if err != nil || task == nil || task.State != "review" {
		return err
	}
	var turns, asked, yours int
	err = s.st.QueryRow("SELECT COUNT(CASE WHEN state IN ('review', 'done') AND by = 'hook' THEN 1 END),"+
		" COUNT(CASE WHEN state = 'needs' AND by = 'hook' THEN 1 END),"+
		" COUNT(CASE WHEN by = 'you' THEN 1 END) FROM history WHERE task_id = ?", id).Scan(&turns, &asked, &yours)
	if err != nil || turns > 1 || asked > 0 || yours > 0 {
		return err
	}
	_, err = s.st.SetState(id, "done", "schedule", store.Fields{"last": "Replaced by the next run", "hidden": true})
	return err
}

func (s *Server) scheduleView(sc store.Schedule, now time.Time) api.Schedule {
	out := api.Schedule{Schedule: sc, Short: board.Short(sc.ID)}
	if next, ok := schedule.Next(sc.At, sc.Days, now); ok && sc.Enabled {
		out.Next = float64(next.Unix())
	}
	if sc.LastTask != "" {
		if task, err := s.st.Task(sc.LastTask); err == nil && task != nil {
			out.LastState = store.StateName(task.State)
		}
	}
	return out
}

func (s *Server) findSchedule(r *http.Request) (*store.Schedule, error) {
	id := r.PathValue("id")
	sc, err := s.st.ResolveSchedule(id)
	switch {
	case errors.Is(err, store.ErrAmbiguous):
		return nil, fail(409, "%v", err)
	case err != nil:
		return nil, err
	case sc == nil:
		return nil, fail(404, "No schedule matches '%s'", id)
	}
	return sc, nil
}

func (s *Server) listSchedules(r *http.Request) (any, error) {
	all, err := s.st.Schedules()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := make([]api.Schedule, 0, len(all))
	for _, sc := range all {
		out = append(out, s.scheduleView(sc, now))
	}
	return out, nil
}

// scheduleFields checks the fields of a schedule that a request may set, and writes them normalized
// into set. Only the fields present in data are checked.
func (s *Server) scheduleFields(data map[string]any, set store.Fields) error {
	if _, ok := data["title"]; ok {
		set["title"] = strings.TrimSpace(str(data, "title"))
	}
	if _, ok := data["prompt"]; ok {
		prompt := strings.TrimSpace(str(data, "prompt"))
		if prompt == "" {
			return fail(400, "A schedule needs a prompt: nobody is there to type it")
		}
		set["prompt"] = prompt
	}
	if _, ok := data["at"]; ok {
		at, err := schedule.NormalizeAt(str(data, "at"))
		if err != nil {
			return fail(400, "%v", err)
		}
		set["at"] = at
	}
	if _, ok := data["days"]; ok {
		days, err := schedule.NormalizeDays(str(data, "days"))
		if err != nil {
			return fail(400, "%v", err)
		}
		set["days"] = days
	}
	if _, ok := data["cwd"]; ok {
		cwd := config.ExpandUser(strings.TrimSpace(str(data, "cwd")))
		if cwd != "" && !isDir(cwd) {
			return fail(400, "'%s' is not a directory", cwd)
		}
		set["cwd"] = cwd
	}
	if _, ok := data["profile"]; ok {
		profile, err := s.checkProfile(str(data, "profile"))
		if err != nil {
			return err
		}
		set["profile"] = profile
	}
	if at, ok := set["at"].(string); ok && schedule.IsCron(at) {
		if days, ok := set["days"]; ok && days != "daily" {
			return fail(400, "A cron line has its own days: leave days out")
		}
	}
	if _, ok := data["timer"]; ok {
		timer, err := timerField(data)
		if err != nil {
			return err
		}
		set["timer"] = timer
	}
	if err := launchFields(data, set); err != nil {
		return err
	}
	for _, flag := range []string{"catch_up", "enabled"} {
		if value, ok := data[flag]; ok {
			set[flag] = truthy(value)
		}
	}
	return nil
}

func (s *Server) createSchedule(r *http.Request) (any, error) {
	data, err := body(r)
	if err != nil {
		return nil, err
	}
	for _, required := range []string{"prompt", "at"} {
		if _, ok := data[required]; !ok {
			data[required] = ""
		}
	}
	if _, ok := data["profile"]; !ok {
		data["profile"] = ""
	}
	set := store.Fields{"days": "daily", "enabled": true, "catch_up": false}
	if err := s.scheduleFields(data, set); err != nil {
		return nil, err
	}
	title, _ := set["title"].(string)
	if title == "" {
		title = board.Clip(board.FirstLine(set["prompt"].(string)), 60)
	}
	cwd, _ := set["cwd"].(string)
	timer, _ := set["timer"].(int)
	model, _ := set["model"].(string)
	effort, _ := set["effort"].(string)
	sc, err := s.st.CreateSchedule(store.Schedule{
		ID: board.NewID(), Title: title, Prompt: set["prompt"].(string), Cwd: cwd, Profile: set["profile"].(string),
		At: set["at"].(string), Days: set["days"].(string), CatchUp: set["catch_up"].(bool), Enabled: set["enabled"].(bool),
		Timer: timer, Model: model, Effort: effort,
	})
	if err != nil {
		return nil, err
	}
	return s.scheduleView(*sc, time.Now()), nil
}

func (s *Server) patchSchedule(r *http.Request) (any, error) {
	sc, err := s.findSchedule(r)
	if err != nil {
		return nil, err
	}
	data, err := body(r)
	if err != nil {
		return nil, err
	}
	set := store.Fields{}
	if err := s.scheduleFields(data, set); err != nil {
		return nil, err
	}
	if title, ok := set["title"]; ok && title == "" {
		return nil, fail(400, "The title cannot be empty")
	}
	if err := s.st.UpdateSchedule(sc.ID, set); err != nil {
		return nil, err
	}
	if sc, err = s.st.Schedule(sc.ID); err != nil {
		return nil, err
	}
	return s.scheduleView(*sc, time.Now()), nil
}

func (s *Server) deleteSchedule(r *http.Request) (any, error) {
	sc, err := s.findSchedule(r)
	if err != nil {
		return nil, err
	}
	return map[string]string{"deleted": sc.ID}, s.st.DeleteSchedule(sc.ID)
}

// runSchedule starts a run now, whatever the time. The next scheduled run still happens.
func (s *Server) runSchedule(r *http.Request) (any, error) {
	sc, err := s.findSchedule(r)
	if err != nil {
		return nil, err
	}
	if err := s.fire(*sc, time.Now()); err != nil {
		return nil, err
	}
	if sc, err = s.st.Schedule(sc.ID); err != nil {
		return nil, err
	}
	if sc.LastError != "" {
		return nil, fail(503, "%s", sc.LastError)
	}
	return s.scheduleView(*sc, time.Now()), nil
}
