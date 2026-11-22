package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/humblebundledore/headcoach/internal/api"
	"github.com/humblebundledore/headcoach/internal/board"
	"github.com/humblebundledore/headcoach/internal/format"
	"github.com/humblebundledore/headcoach/internal/schedule"
)

// scheduleSteps are the steps of the new-schedule flow after its folder and profile: the time, the
// days, catch-up, the title and the prompt. A cron line skips the days.
const scheduleSteps = 5

// scheduleDraft is what the new-schedule flow has gathered so far.
type scheduleDraft struct {
	at, days, title string
	catchUp         bool
}

type (
	// schedulesMsg carries the list of schedules, after a poll or an action on one.
	schedulesMsg struct {
		list   []api.Schedule
		notice string
		err    error
	}
	// catchUpMsg carries the answer to the catch-up question of the new-schedule flow.
	catchUpMsg struct{ on bool }
)

var scheduleColumns = []column{
	{"ID", false}, {"TITLE", false}, {"AT", false}, {"DAYS", false}, {"CATCH-UP", false}, {"STATUS", false},
	{"NEXT", false}, {"LAST RUN", false}, {"FOLDER", false},
}

const scheduleHelp = `j / k, arrows   move the cursor
g / G           go to the top or the bottom
n               new schedule: folder, profile, time, days, catch-up, title, prompt;
                the time is HH:MM, or a cron line such as */15 * * * *
space           pause or resume
c               catch-up on or off: when the machine was asleep or off at the time,
                run once it is back (on), or skip until the next time (off)
r               run now, whatever the time; the next scheduled run still happens
enter           show the task of the last run on the board
x               delete the schedule (the tasks it started are kept)
esc or q        back to the board`

func (m model) loadSchedules() tea.Cmd {
	c := m.client
	return func() tea.Msg {
		var list []api.Schedule
		err := c.Call("GET", "/api/schedules", nil, &list)
		return schedulesMsg{list: list, err: err}
	}
}

// scheduleAct calls the daemon about a schedule, then reloads the list. notice builds the feedback
// from the schedule the daemon answered with.
func (m model) scheduleAct(method, path string, in any, notice func(sc api.Schedule) string) tea.Cmd {
	c := m.client
	return func() tea.Msg {
		var sc api.Schedule
		if err := c.Call(method, path, in, &sc); err != nil {
			return schedulesMsg{err: err}
		}
		var list []api.Schedule
		err := c.Call("GET", "/api/schedules", nil, &list)
		return schedulesMsg{list: list, notice: notice(sc), err: err}
	}
}

func (m model) currentSchedule() *api.Schedule {
	if m.schedCursor < 0 || m.schedCursor >= len(m.schedules) {
		return nil
	}
	return &m.schedules[m.schedCursor]
}

// schedulesKey handles a key on the table of schedules.
func (m model) schedulesKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "Q":
		return m, tea.Quit
	case "esc", "q":
		m.view = viewBoard
		return m, nil
	case "j", "down":
		m.schedCursor = min(m.schedCursor+1, len(m.schedules)-1)
		return m, nil
	case "k", "up":
		m.schedCursor = max(m.schedCursor-1, 0)
		return m, nil
	case "g", "home":
		m.schedCursor = 0
		return m, nil
	case "G", "end":
		m.schedCursor = max(0, len(m.schedules)-1)
		return m, nil
	case "n":
		m.draft = scheduleDraft{}
		return m, m.askFolder("schedule")
	case "?":
		m.note("Schedule keys (any key closes)", scheduleHelp, nil)
		return m, nil
	}
	sc := m.currentSchedule()
	if sc == nil {
		return m, nil
	}
	path := "/api/schedules/" + sc.ID
	now := time.Now()
	switch key {
	case " ", "p":
		return m, m.scheduleAct("PATCH", path, map[string]bool{"enabled": !sc.Enabled}, func(s api.Schedule) string {
			if !s.Enabled {
				return s.Title + " paused"
			}
			return s.Title + " resumed, next run " + format.When(s.Next, now)
		})
	case "c":
		return m, m.scheduleAct("PATCH", path, map[string]bool{"catch_up": !sc.CatchUp}, func(s api.Schedule) string {
			if s.CatchUp {
				return s.Title + ": a missed run now starts once the machine is back"
			}
			return s.Title + ": a missed run is now skipped"
		})
	case "r":
		return m, m.scheduleAct("POST", path+"/run", nil, func(s api.Schedule) string {
			return s.Title + " started as " + board.Short(s.LastTask) + "; enter shows it"
		})
	case "enter":
		if sc.LastTask == "" || sc.LastState == "" {
			m.say(sc.Title+" has no task on the board", true)
			return m, nil
		}
		m.view, m.jump, m.showDone = viewBoard, sc.LastTask, true
		m.rebuild()
		return m, nil
	case "x":
		title := sc.Title
		m.note("Delete", "Delete the schedule "+title+"? The tasks it started stay.  y / n", func() tea.Cmd {
			return m.scheduleAct("DELETE", path, nil, func(api.Schedule) string { return title + " deleted" })
		})
	}
	return m, nil
}

// cronDraft tells whether the new schedule runs on a cron line, which has no days step.
func (m model) cronDraft() bool { return schedule.IsCron(m.draft.at) }

// step is the number of a step of the new-schedule flow that comes after the folder and profile.
func (m model) step(after int) int {
	if after > 2 && m.cronDraft() {
		after--
	}
	return 1 + m.profileStep() + after
}

func (m *model) askScheduleTime() tea.Cmd {
	return m.ask("schedule-at", fmt.Sprintf("New schedule %d/%d, the time of day, HH:MM, or a cron line such as */15 * * * *. Folder: %s",
		m.step(1), m.newSteps(), m.newDir), "08:00")
}

func (m *model) askScheduleDays() tea.Cmd {
	return m.ask("schedule-days", fmt.Sprintf("New schedule %d/%d, the days: daily, weekdays, weekends, or mon,wed,fri",
		m.step(2), m.newSteps()), "daily")
}

func (m *model) askScheduleTitle() tea.Cmd {
	return m.ask("schedule-title", fmt.Sprintf("New schedule %d/%d, the title. Empty takes the first line of the prompt",
		m.step(4), m.newSteps()), "")
}

func (m *model) askSchedulePrompt() tea.Cmd {
	return m.ask("schedule-prompt", fmt.Sprintf("New schedule %d/%d, the prompt each run starts with",
		m.step(5), m.newSteps()), "")
}

// askCatchUp asks whether a run the machine missed at its time, when, starts once it is back.
func (m *model) askCatchUp(when string) {
	m.note(fmt.Sprintf("New schedule %d/%d, catch-up", m.step(3), m.newSteps()),
		"If the machine is asleep or off at "+when+", run it once it is back, however late?\n\n"+
			"  y  yes: a missed run starts as soon as the machine is back\n"+
			"  n  no: only at "+when+"; a missed run waits for the next one",
		func() tea.Cmd { return func() tea.Msg { return catchUpMsg{true} } })
	m.deny = func() tea.Cmd { return func() tea.Msg { return catchUpMsg{false} } }
}

// scheduleStep takes the answer to one step of the new-schedule flow and asks the next one.
// A wrong answer asks the same step again, with what was typed.
func (m model) scheduleStep(kind, text string) (tea.Model, tea.Cmd) {
	switch kind {
	case "schedule-at":
		at, err := schedule.NormalizeAt(text)
		if err != nil {
			m.say(err.Error(), true)
			cmd := m.askScheduleTime()
			m.input.SetValue(text)
			return m, cmd
		}
		m.draft.at = at
		if schedule.IsCron(at) {
			m.draft.days = "daily"
			m.askCatchUp("its time")
			return m, nil
		}
		return m, m.askScheduleDays()
	case "schedule-days":
		days, err := schedule.NormalizeDays(text)
		if err != nil {
			m.say(err.Error(), true)
			cmd := m.askScheduleDays()
			m.input.SetValue(text)
			return m, cmd
		}
		m.draft.days = days
		m.askCatchUp(m.draft.at)
		return m, nil
	case "schedule-title":
		m.draft.title = text
		return m, m.askSchedulePrompt()
	case "schedule-prompt":
		if text == "" {
			m.say("A schedule needs a prompt: nobody is there to type it", true)
			return m, m.askSchedulePrompt()
		}
		data := map[string]any{
			"title": m.draft.title, "prompt": text, "cwd": m.newDir, "at": m.draft.at, "days": m.draft.days,
			"catch_up": m.draft.catchUp,
		}
		if m.newProfile != "" {
			data["profile"] = m.newProfile
		}
		now := time.Now()
		return m, m.scheduleAct("POST", "/api/schedules", data, func(s api.Schedule) string {
			return "Schedule " + s.Title + " added, next run " + format.When(s.Next, now)
		})
	}
	return m, nil
}

// scheduleCells are the cells of one row of the table of schedules.
func scheduleCells(sc api.Schedule, now time.Time) []cell {
	catchUp := c("no", dim)
	if sc.CatchUp {
		catchUp = c("yes", plain)
	}
	status := c("on", levelStyles["ok"])
	if !sc.Enabled {
		status = c("paused", dim)
	}
	last := c(format.LastRun(sc, now), plain)
	if sc.LastError != "" {
		last.style = alarm
	} else if sc.LastRun == 0 {
		last.style = dim
	}
	return []cell{
		c(sc.Short, plain), c(board.Clip(format.Timed(sc.Title, sc.Timer), 32), plain), c(sc.At, bold), c(format.ScheduleDays(sc.At, sc.Days), plain),
		catchUp, status, c(format.When(sc.Next, now), plain), last, c(board.Clip(sc.Cwd, 40), dim),
	}
}

func (m model) schedulesView() string {
	now := time.Now()
	var rows [][]cell
	for _, sc := range m.schedules {
		rows = append(rows, scheduleCells(sc, now))
	}
	// The rows scroll with the cursor, in columns sized from every row so they hold still.
	height := m.tableHeight()
	top := max(0, m.schedCursor-height+1)
	widths := columnWidths(scheduleColumns, rows)
	table := tableLines(scheduleColumns, widths, rows[top:min(len(rows), top+height)], m.schedCursor-top, max(10, m.width-2))
	if len(m.schedules) == 0 {
		table = append(table, dim.Render(" No schedules yet. n creates one."))
	}
	title := []cell{c("schedules", stateStyles["parked"].Bold(true)), c(fmt.Sprintf("[%d]", len(m.schedules)), plain)}
	return m.frame(title, table, []cell{c("A run is a task on the board with its schedule in the ↻ column, titled with the schedule and its day, plus the time for a cron line. The next run moves it to Done if you left it in Review.", dim)})
}
