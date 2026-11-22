package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/humblebundledore/headcoach/internal/api"
	"github.com/humblebundledore/headcoach/internal/board"
	"github.com/humblebundledore/headcoach/internal/format"
	"github.com/humblebundledore/headcoach/internal/schedule"
)

func cmdSchedule(e *env, p parsed) error {
	action := "ls"
	if len(p.args) > 0 {
		action = p.args[0]
	}
	args := p.args[min(1, len(p.args)):]
	switch action {
	case "ls":
		return scheduleList(e, p)
	case "add":
		return scheduleAdd(e, p, args)
	case "run", "pause", "resume", "rm":
		if len(args) != 1 {
			return usageError{fmt.Sprintf("usage: hc schedule %s <schedule>", action)}
		}
		return scheduleAct(e, action, args[0])
	case "timer":
		if len(args) != 2 {
			return usageError{"usage: hc schedule timer <schedule> <duration|off>"}
		}
		return scheduleTimer(e, args[0], args[1])
	case "model", "effort":
		if len(args) != 2 {
			return usageError{fmt.Sprintf("usage: hc schedule %s <schedule> <%s|default>", action, action)}
		}
		return scheduleLaunch(e, action, args[0], args[1])
	}
	return usageError{fmt.Sprintf("'%s' is not one of: ls, add, run, pause, resume, timer, model, effort, rm", action)}
}

func scheduleList(e *env, p parsed) error {
	var all []api.Schedule
	if err := e.client.Call("GET", "/api/schedules", nil, &all); err != nil {
		return err
	}
	if p.has("json") {
		return e.json(all)
	}
	if len(all) == 0 {
		e.printf("No schedules. Add one with: hc schedule add --at 08:00 \"<prompt>\" or --cron '*/15 * * * *'\n")
		return nil
	}
	width := 5
	for _, sc := range all {
		width = max(width, len(sc.At))
	}
	const columns = "%-9s %-24s %-*s %-14s %-8s %-*s %-6s %-7s %-16s %s\n"
	models := 5
	for _, sc := range all {
		models = max(models, len(sc.Model))
	}
	e.printf("%s", e.paint(fmt.Sprintf(columns, "ID", "TITLE", width, "AT", "DAYS", "CATCH-UP", models, "MODEL", "EFFORT",
		"STATUS", "NEXT", "LAST RUN"), "dim"))
	now := time.Now()
	for _, sc := range all {
		catchUp, status := "no", "on"
		if sc.CatchUp {
			catchUp = "yes"
		}
		if !sc.Enabled {
			status = "paused"
		}
		e.printf(columns, sc.Short, cut(format.Timed(sc.Title, sc.Timer), 24), width, sc.At, format.ScheduleDays(sc.At, sc.Days), catchUp,
			models, orDash(sc.Model), orDash(sc.Effort), status,
			format.When(sc.Next, now), format.LastRun(sc, now))
	}
	return nil
}

func scheduleAdd(e *env, p parsed, words []string) error {
	at, cron := p.get("at", ""), p.get("cron", "")
	if (at == "") == (cron == "") || len(words) == 0 {
		return usageError{"usage: hc schedule add --at HH:MM [--days ...] | --cron '*/15 * * * *' [--catch-up] <prompt>..."}
	}
	if cron != "" {
		at = cron
	}
	if schedule.IsCron(at) && p.get("days", "") != "" {
		return usageError{"--days does not go with a cron line: it has its own days"}
	}
	data := map[string]any{
		"prompt": strings.Join(words, " "), "title": p.get("title", ""), "at": at, "days": p.get("days", "daily"),
		"catch_up": p.has("catch-up"), "profile": p.get("profile", ""),
	}
	if p.has("timer") {
		timer, err := parseTimer(p.get("timer", ""))
		if err != nil {
			return err
		}
		data["timer"] = timer
	}
	launchOptions(p, data)
	if dir := p.get("dir", ""); dir != "" {
		resolved, err := newTaskDir(e, dir)
		if err != nil {
			return err
		}
		data["cwd"] = resolved
	}
	var sc api.Schedule
	if err := e.client.Call("POST", "/api/schedules", data, &sc); err != nil {
		return err
	}
	e.printf("%s  %s  next run %s\n", sc.Short, sc.Title, format.When(sc.Next, time.Now()))
	return nil
}

// scheduleTimer sets the timer of the runs a schedule starts from now on.
func scheduleTimer(e *env, id, text string) error {
	timer, err := parseTimer(text)
	if err != nil {
		return err
	}
	var sc api.Schedule
	if err := e.client.Call("PATCH", "/api/schedules/"+id, map[string]int{"timer": timer}, &sc); err != nil {
		return err
	}
	if sc.Timer == 0 {
		e.printf("%s  its runs have no timer\n", sc.Short)
		return nil
	}
	e.printf("%s  each run closes after %s\n", sc.Short, format.Human(sc.Timer))
	return nil
}

// scheduleLaunch sets the model or the effort the runs of a schedule start with from now on.
// default goes back to the profile's.
func scheduleLaunch(e *env, field, id, value string) error {
	var sc api.Schedule
	if err := e.client.Call("PATCH", "/api/schedules/"+id, map[string]string{field: value}, &sc); err != nil {
		return err
	}
	value = sc.Model
	if field == "effort" {
		value = sc.Effort
	}
	e.printf("%s  its runs start with %s %s\n", sc.Short, field, orDefault(value))
	return nil
}

func scheduleAct(e *env, action, id string) error {
	var sc api.Schedule
	var err error
	switch action {
	case "run":
		err = e.client.Call("POST", "/api/schedules/"+id+"/run", nil, &sc)
	case "pause", "resume":
		err = e.client.Call("PATCH", "/api/schedules/"+id, map[string]bool{"enabled": action == "resume"}, &sc)
	case "rm":
		var out struct {
			Deleted string `json:"deleted"`
		}
		if err := e.client.Call("DELETE", "/api/schedules/"+id, nil, &out); err != nil {
			return err
		}
		e.printf("deleted %s\n", board.Short(out.Deleted))
		return nil
	}
	if err != nil {
		return err
	}
	switch action {
	case "run":
		e.printf("%s  started task %s\n", sc.Short, board.Short(sc.LastTask))
	case "pause":
		e.printf("%s  paused\n", sc.Short)
	default:
		e.printf("%s  next run %s\n", sc.Short, format.When(sc.Next, time.Now()))
	}
	return nil
}
