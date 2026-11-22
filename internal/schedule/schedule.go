// Package schedule decides when a schedule is due. Times are local: 08:00 means 08:00 on this machine.
// A schedule's time is a time of day such as 08:00, or a cron line such as */15 * * * *.
package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/humblebundledore/headcoach/internal/store"
)

// Grace is how late a schedule without catch-up may still start. Past it, the run is skipped.
const Grace = 5 * time.Minute

// days in the order of time.Weekday.
var dayNames = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// weekOrder is how a list of days is written back: Monday first.
var weekOrder = []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday}

// ParseAt reads a time of day written H:MM or HH:MM.
func ParseAt(at string) (hour, minute int, err error) {
	h, m, ok := strings.Cut(strings.TrimSpace(at), ":")
	hour, err1 := strconv.Atoi(h)
	minute, err2 := strconv.Atoi(m)
	if !ok || err1 != nil || err2 != nil || len(m) != 2 || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("'%s' is not a time of day such as 08:00", at)
	}
	return hour, minute, nil
}

// NormalizeAt writes a time of day as HH:MM, or a cron line with single spaces.
func NormalizeAt(at string) (string, error) {
	if IsCron(at) {
		c, err := parseCron(at)
		if err != nil {
			return "", err
		}
		if _, ok := c.next(time.Now()); !ok {
			return "", fmt.Errorf("the cron line '%s' never matches a day", at)
		}
		return strings.ToLower(strings.Join(strings.Fields(at), " ")), nil
	}
	h, m, err := ParseAt(at)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%02d:%02d", h, m), nil
}

// NormalizeDays reads the days a schedule runs on: daily, weekdays, weekends, or a list of days
// such as mon,wed,fri. Empty means daily.
func NormalizeDays(days string) (string, error) {
	days = strings.ToLower(strings.TrimSpace(days))
	switch days {
	case "", "daily", "everyday", "every day":
		return "daily", nil
	case "weekdays", "weekends":
		return days, nil
	}
	picked := map[time.Weekday]bool{}
	for _, word := range strings.FieldsFunc(days, func(r rune) bool { return r == ',' || r == ' ' }) {
		found := false
		for i, name := range dayNames {
			if len(word) >= 3 && strings.HasPrefix(word, name) && strings.HasPrefix(fullDay(i), word) {
				picked[time.Weekday(i)], found = true, true
			}
		}
		if !found {
			return "", fmt.Errorf("'%s' is not a day. Use daily, weekdays, weekends, or days such as mon,wed,fri", word)
		}
	}
	var out []string
	for _, day := range weekOrder {
		if picked[day] {
			out = append(out, dayNames[day])
		}
	}
	if len(out) == 7 {
		return "daily", nil
	}
	return strings.Join(out, ","), nil
}

func fullDay(i int) string { return strings.ToLower(time.Weekday(i).String()) }

func allowed(days string, day time.Weekday) bool {
	switch days {
	case "daily", "":
		return true
	case "weekdays":
		return day != time.Saturday && day != time.Sunday
	case "weekends":
		return day == time.Saturday || day == time.Sunday
	}
	for _, name := range strings.Split(days, ",") {
		if name == dayNames[day] {
			return true
		}
	}
	return false
}

// occurrence is the schedule's time on the day that is offset days away from now.
func occurrence(now time.Time, offset, hour, minute int) time.Time {
	d := now.AddDate(0, 0, offset)
	return time.Date(d.Year(), d.Month(), d.Day(), hour, minute, 0, 0, now.Location())
}

// Latest is the last time the schedule was due, at or before now. A cron line ignores days.
func Latest(at, days string, now time.Time) (time.Time, bool) {
	if IsCron(at) {
		c, err := parseCron(at)
		if err != nil {
			return time.Time{}, false
		}
		return c.latest(now)
	}
	h, m, err := ParseAt(at)
	if err != nil {
		return time.Time{}, false
	}
	for offset := 0; offset >= -7; offset-- {
		t := occurrence(now, offset, h, m)
		if !t.After(now) && allowed(days, t.Weekday()) {
			return t, true
		}
	}
	return time.Time{}, false
}

// Next is the next time the schedule is due, after now. A cron line ignores days.
func Next(at, days string, now time.Time) (time.Time, bool) {
	if IsCron(at) {
		c, err := parseCron(at)
		if err != nil {
			return time.Time{}, false
		}
		return c.next(now)
	}
	h, m, err := ParseAt(at)
	if err != nil {
		return time.Time{}, false
	}
	for offset := 0; offset <= 7; offset++ {
		t := occurrence(now, offset, h, m)
		if t.After(now) && allowed(days, t.Weekday()) {
			return t, true
		}
	}
	return time.Time{}, false
}

func sameMinute(a, b time.Time) bool {
	return a.Format("2006-01-02 15:04") == b.Format("2006-01-02 15:04")
}

// Due tells whether a schedule should start a task now.
//
// It is due when its last time has passed and it has not run since. With catch-up, how late does not
// matter: a board that slept through 08:00 runs it when it wakes, once, however many days it missed.
// Without catch-up it runs only within Grace of its time. A time before the schedule existed is never due.
func Due(sc store.Schedule, now time.Time) bool {
	if !sc.Enabled {
		return false
	}
	due, ok := Latest(sc.At, sc.Days, now)
	if !ok {
		return false
	}
	at := float64(due.Unix())
	if sc.LastRun >= at || sc.CreatedAt > at {
		return false
	}
	// The night the clocks go back, a cron line's 02:30 comes twice: one run is enough.
	if IsCron(sc.At) && sc.LastRun > 0 && sameMinute(time.Unix(int64(sc.LastRun), 0).In(now.Location()), due) {
		return false
	}
	return sc.CatchUp || now.Sub(due) <= Grace
}
