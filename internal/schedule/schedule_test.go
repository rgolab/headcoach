package schedule

import (
	"testing"
	"time"

	"github.com/humblebundledore/headcoach/internal/store"
)

// at is a moment on Monday 2026-11-16 (or days later), local time.
func at(day, hour, minute int) time.Time {
	return time.Date(2026, 11, 16+day, hour, minute, 0, 0, time.Local)
}

func unix(t time.Time) float64 { return float64(t.Unix()) }

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{"8:00": "08:00", "08:05": "08:05", "23:59": "23:59"} {
		if got, err := NormalizeAt(in); err != nil || got != want {
			t.Errorf("NormalizeAt(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "8", "8:5", "24:00", "12:60", "ab:cd"} {
		if _, err := NormalizeAt(bad); err == nil {
			t.Errorf("NormalizeAt(%q) was accepted", bad)
		}
	}
	for in, want := range map[string]string{
		"": "daily", "Daily": "daily", "weekdays": "weekdays", "weekends": "weekends",
		"fri,mon": "mon,fri", "Monday, wednesday": "mon,wed", "mon,tue,wed,thu,fri,sat,sun": "daily",
	} {
		if got, err := NormalizeDays(in); err != nil || got != want {
			t.Errorf("NormalizeDays(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"mo", "funday", "mon,xyz"} {
		if _, err := NormalizeDays(bad); err == nil {
			t.Errorf("NormalizeDays(%q) was accepted", bad)
		}
	}
}

func TestLatestAndNext(t *testing.T) {
	now := at(0, 9, 0) // Monday 09:00
	if got, _ := Latest("08:00", "daily", now); !got.Equal(at(0, 8, 0)) {
		t.Errorf("latest daily = %v", got)
	}
	if got, _ := Next("08:00", "daily", now); !got.Equal(at(1, 8, 0)) {
		t.Errorf("next daily = %v", got)
	}
	// Weekends only: from a Monday, the last was Sunday, the next is Saturday.
	if got, _ := Latest("08:00", "weekends", now); !got.Equal(at(-1, 8, 0)) {
		t.Errorf("latest weekends = %v", got)
	}
	if got, _ := Next("08:00", "weekends", now); !got.Equal(at(5, 8, 0)) {
		t.Errorf("next weekends = %v", got)
	}
}

func TestDue(t *testing.T) {
	created := unix(at(-3, 12, 0)) // made on Friday
	daily := func(catchUp bool, lastRun float64) store.Schedule {
		return store.Schedule{At: "08:00", Days: "daily", CatchUp: catchUp, Enabled: true, LastRun: lastRun, CreatedAt: created}
	}
	ranSunday := unix(at(-1, 8, 0))
	for _, c := range []struct {
		name string
		sc   store.Schedule
		now  time.Time
		want bool
	}{
		{"on time", daily(false, ranSunday), at(0, 8, 0), true},
		{"before its time", daily(true, ranSunday), at(0, 7, 59), false},
		{"already ran today", daily(true, unix(at(0, 8, 1))), at(0, 10, 0), false},
		{"a little late, no catch-up", daily(false, ranSunday), at(0, 8, 4), true},
		{"woke late, no catch-up: skipped", daily(false, ranSunday), at(0, 10, 0), false},
		{"woke late with catch-up: runs", daily(true, ranSunday), at(0, 10, 0), true},
		{"off for days with catch-up: runs once", daily(true, unix(at(-3, 8, 0))), at(0, 10, 0), true},
		{"paused", store.Schedule{At: "08:00", Days: "daily", CatchUp: true, LastRun: ranSunday, CreatedAt: created}, at(0, 10, 0), false},
		{"made after today's time", store.Schedule{At: "08:00", Days: "daily", CatchUp: true, Enabled: true, CreatedAt: unix(at(0, 9, 0))}, at(0, 10, 0), false},
	} {
		if got := Due(c.sc, c.now); got != c.want {
			t.Errorf("%s: due = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCron(t *testing.T) {
	for in, want := range map[string]string{
		"*/15 * * * *": "*/15 * * * *", "0  9 * *  MON-FRI": "0 9 * * mon-fri", "30 8 1,15 jan *": "30 8 1,15 jan *",
	} {
		if got, err := NormalizeAt(in); err != nil || got != want {
			t.Errorf("NormalizeAt(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"* * * *", "* * * * * *", "60 * * * *", "* 24 * * *", "* * 0 * *", "*/0 * * * *",
		"5-1 * * * *", "* * * foo *", "a * * * *", "0 0 31 2 *"} {
		if _, err := NormalizeAt(bad); err == nil {
			t.Errorf("NormalizeAt(%q) was accepted", bad)
		}
	}
	now := at(0, 9, 7) // Monday 09:07
	for _, c := range []struct {
		line         string
		latest, next time.Time
	}{
		{"*/15 * * * *", at(0, 9, 0), at(0, 9, 15)},
		{"0 9 * * mon-fri", at(0, 9, 0), at(1, 9, 0)},
		{"0 9 * * sat,sun", at(-1, 9, 0), at(5, 9, 0)},
		{"0 9 * * 7", at(-1, 9, 0), at(6, 9, 0)},
		{"30 22 * * *", at(-1, 22, 30), at(0, 22, 30)},
		{"5-10/5 9 * * *", at(0, 9, 5), at(0, 9, 10)},
		// Both day fields restricted: either one is enough. The 16th is a Monday, the 20th a Friday.
		{"0 8 20 * mon", at(0, 8, 0), at(4, 8, 0)},
		// A day field starting with * is unrestricted: odd days that are Mondays. The 23rd is a Monday.
		{"0 8 */2 * mon", time.Date(2026, 11, 9, 8, 0, 0, 0, time.Local), at(7, 8, 0)},
		{"0 0 1 1 *", time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local), time.Date(2027, 1, 1, 0, 0, 0, 0, time.Local)},
	} {
		if got, ok := Latest(c.line, "daily", now); !ok || !got.Equal(c.latest) {
			t.Errorf("Latest(%q) = %v, %v; want %v", c.line, got, ok, c.latest)
		}
		if got, ok := Next(c.line, "daily", now); !ok || !got.Equal(c.next) {
			t.Errorf("Next(%q) = %v, %v; want %v", c.line, got, ok, c.next)
		}
	}
	if _, ok := Next("0 0 31 2 *", "daily", now); ok {
		t.Error("February 31st was found")
	}
	every := store.Schedule{At: "*/15 * * * *", Days: "daily", Enabled: true, CreatedAt: unix(at(0, 8, 0)), LastRun: unix(at(0, 9, 0))}
	if Due(every, at(0, 9, 14)) || !Due(every, at(0, 9, 15)) {
		t.Error("a cron schedule is not due on its next minute")
	}
}

func TestCronOnTheNightClocksGoBack(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip(err)
	}
	first := time.Date(2026, 10, 25, 2, 30, 0, 0, berlin) // 02:30 summer time
	second := first.Add(time.Hour)                        // 02:30 again, winter time
	sc := store.Schedule{At: "30 2 * * *", Days: "daily", Enabled: true, CreatedAt: unix(first.AddDate(0, 0, -1))}
	if !Due(sc, first) {
		t.Fatal("the first 02:30 is not due")
	}
	sc.LastRun = unix(first.Add(10 * time.Second))
	if Due(sc, second) {
		t.Error("02:30 runs twice the night the clocks go back")
	}
}
