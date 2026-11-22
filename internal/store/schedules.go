package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Schedule starts a task with the same prompt at a time of day, or on a cron line.
type Schedule struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Prompt  string `json:"prompt"`
	Cwd     string `json:"cwd"`
	Profile string `json:"profile"`
	At      string `json:"at"`   // HH:MM in local time, or a cron line
	Days    string `json:"days"` // daily, weekdays, weekends, or a list such as mon,wed,fri
	// CatchUp runs a schedule whose time passed while the board was asleep, once it wakes.
	// Without it a missed run is skipped.
	CatchUp   bool    `json:"catch_up"`
	Enabled   bool    `json:"enabled"`
	LastRun   float64 `json:"last_run"`
	LastTask  string  `json:"last_task"`
	LastError string  `json:"last_error"`
	// Timer is the timer, in seconds, of each task the schedule starts. Zero: its runs have none.
	Timer int `json:"timer"`
	// Model and Effort are what its runs start Claude Code with. Empty: the profile's own.
	Model     string  `json:"model"`
	Effort    string  `json:"effort"`
	CreatedAt float64 `json:"created_at"`
}

const scheduleColumns = "id, title, prompt, cwd, profile, at, days, catch_up, enabled, last_run, last_task, last_error," +
	" timer, model, effort, created_at"

func scanSchedule(row scanner) (*Schedule, error) {
	var sc Schedule
	err := row.Scan(&sc.ID, &sc.Title, &sc.Prompt, &sc.Cwd, &sc.Profile, &sc.At, &sc.Days, &sc.CatchUp, &sc.Enabled,
		&sc.LastRun, &sc.LastTask, &sc.LastError, &sc.Timer, &sc.Model, &sc.Effort, &sc.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sc, nil
}

// CreateSchedule inserts a schedule. Its creation time is now: no run before it is ever due.
func (s *Store) CreateSchedule(sc Schedule) (*Schedule, error) {
	sc.CreatedAt = Now()
	_, err := s.Exec("INSERT INTO schedules ("+scheduleColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		sc.ID, sc.Title, sc.Prompt, sc.Cwd, sc.Profile, sc.At, sc.Days, sc.CatchUp, sc.Enabled,
		sc.LastRun, sc.LastTask, sc.LastError, sc.Timer, sc.Model, sc.Effort, sc.CreatedAt)
	if err != nil {
		return nil, err
	}
	return s.Schedule(sc.ID)
}

// Schedule returns the schedule with that id, or nil when there is none.
func (s *Store) Schedule(id string) (*Schedule, error) {
	return scanSchedule(s.QueryRow("SELECT "+scheduleColumns+" FROM schedules WHERE id = ?", id))
}

// Schedules lists every schedule, sorted by their at field as text, then by title.
func (s *Store) Schedules() ([]Schedule, error) {
	rows, err := s.Query("SELECT " + scheduleColumns + " FROM schedules ORDER BY at, title")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Schedule{}
	for rows.Next() {
		sc, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sc)
	}
	return out, rows.Err()
}

// ResolveSchedule finds a schedule by its id or a unique prefix of it.
func (s *Store) ResolveSchedule(prefix string) (*Schedule, error) {
	exact, err := s.Schedule(prefix)
	if err != nil || exact != nil || prefix == "" {
		return exact, err
	}
	rows, err := s.Query("SELECT "+scheduleColumns+" FROM schedules WHERE substr(id, 1, ?) = ? LIMIT 2",
		len([]rune(prefix)), prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var found []Schedule
	for rows.Next() {
		sc, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		found = append(found, *sc)
	}
	if err := rows.Err(); err != nil || len(found) == 0 {
		return nil, err
	}
	if len(found) > 1 {
		return nil, fmt.Errorf("'%s' %w", prefix, ErrAmbiguous)
	}
	return &found[0], nil
}

// scheduleFields are the columns UpdateSchedule may change.
var scheduleFields = map[string]bool{
	"title": true, "prompt": true, "cwd": true, "profile": true, "at": true, "days": true, "catch_up": true,
	"enabled": true, "last_run": true, "last_task": true, "last_error": true, "timer": true,
	"model": true, "effort": true,
}

// UpdateSchedule changes some columns of a schedule.
func (s *Store) UpdateSchedule(id string, fields Fields) error {
	names := make([]string, 0, len(fields))
	for name := range fields {
		if !scheduleFields[name] {
			return fmt.Errorf("unknown schedule column '%s'", name)
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	sets := make([]string, 0, len(names))
	args := make([]any, 0, len(names)+1)
	for _, name := range names {
		sets = append(sets, name+" = ?")
		args = append(args, fields[name])
	}
	_, err := s.Exec("UPDATE schedules SET "+strings.Join(sets, ", ")+" WHERE id = ?", append(args, id)...)
	return err
}

// DeleteSchedule removes a schedule. The tasks it started stay.
func (s *Store) DeleteSchedule(id string) error {
	_, err := s.Exec("DELETE FROM schedules WHERE id = ?", id)
	return err
}
