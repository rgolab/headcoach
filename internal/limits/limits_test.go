package limits

import (
	"testing"

	"github.com/rouxlabs/headcoach/internal/store"
)

const (
	now = 1_000_000.0
	h   = 3600.0
)

type obj = map[string]any

func win(pct, resetsAt float64) obj { return obj{"used_percentage": pct, "resets_at": resetsAt} }

func open(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func record(t *testing.T, st *store.Store, profile string, limits any, at float64) {
	t.Helper()
	if err := Record(st, profile, limits, at); err != nil {
		t.Fatal(err)
	}
}

func report(t *testing.T, st *store.Store, at float64) []Entry {
	t.Helper()
	out, err := Report(st, at)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func limitIn(e Entry) int {
	if e.LimitIn == nil {
		return -1
	}
	return *e.LimitIn
}

func TestNoWarningAtAPaceThatFits(t *testing.T) {
	st := open(t)
	// 20% used, 2h30 into the window: the average pace ends at 40%.
	record(t, st, "work", obj{"five_hour": win(20, now+2.5*h)}, now)
	got := report(t, st, now)
	if len(got) != 1 {
		t.Fatalf("%d entries", len(got))
	}
	e := got[0]
	if e.Profile != "work" || e.Label != "5h" || e.Pct != 20 || e.ResetsIn != 9000 || e.LimitIn != nil || e.Level != "ok" || e.Stale {
		t.Errorf("entry = %+v", e)
	}
}

func TestWarningFromTheAveragePaceOfTheWindow(t *testing.T) {
	st := open(t)
	// 80% used one hour into the window: 20% more takes 15 minutes, the reset is 4 hours away.
	record(t, st, "work", obj{"five_hour": win(80, now+4*h)}, now)
	if e := report(t, st, now)[0]; limitIn(e) != 900 || e.Level != "hot" {
		t.Errorf("entry = %+v, limit in %d", e, limitIn(e))
	}
}

func TestRecentPaceWinsOverTheAverage(t *testing.T) {
	st := open(t)
	reset := now + h
	// A quiet window (10% in four hours) that started burning 30% in the last 20 minutes.
	record(t, st, "work", obj{"five_hour": win(10, reset)}, now-1200)
	record(t, st, "work", obj{"five_hour": win(40, reset)}, now)
	// And the other way round: a window that burned fast early and has been idle since gives no warning.
	record(t, st, "personal", obj{"five_hour": win(90, reset+60)}, now-1200)
	record(t, st, "personal", obj{"five_hour": win(90, reset+60)}, now)
	got := report(t, st, now)
	if got[0].Profile != "personal" || got[0].LimitIn != nil || got[0].Level != "hot" { // no pace, but 90% is red on its own
		t.Errorf("personal = %+v", got[0])
	}
	if limitIn(got[1]) != 2400 { // 60% left at 1.5% a minute
		t.Errorf("work runs out in %d", limitIn(got[1]))
	}
}

func TestLevelsAndBothWindows(t *testing.T) {
	st := open(t)
	// 5% in the first six minutes would be 250% at that pace, but a window that young is not extrapolated.
	record(t, st, "work", obj{"five_hour": win(5, now+4.9*h), "seven_day": win(70, now+86400)}, now)
	got := report(t, st, now)
	if got[0].Label != "5h" || got[0].Level != "ok" || got[0].LimitIn != nil {
		t.Errorf("5h = %+v", got[0])
	}
	if got[1].Label != "7d" || got[1].Level != "warm" || got[1].LimitIn != nil {
		t.Errorf("7d = %+v", got[1])
	}
}

func TestAWindowThatHasResetIsDroppedAndOldFiguresGoStale(t *testing.T) {
	st := open(t)
	record(t, st, "work", obj{"five_hour": win(50, now+60), "seven_day": win(10, now+6*86400)}, now)
	later := report(t, st, now+1200)
	if len(later) != 1 || later[0].Label != "7d" || !later[0].Stale {
		t.Errorf("later = %+v", later)
	}
}

func TestSamplesAreThinnedAndJunkIsIgnored(t *testing.T) {
	st := open(t)
	for i := 0.0; i < 10; i++ {
		record(t, st, "work", obj{"five_hour": win(10+i, now+h)}, now+i)
	}
	var samples int
	st.QueryRow("SELECT count(*) FROM limit_samples").Scan(&samples)
	if samples != 1 {
		t.Errorf("%d samples in ten seconds", samples)
	}
	record(t, st, "work", "junk", now)
	record(t, st, "work", nil, now)
	record(t, st, "work", obj{"five_hour": obj{"used_percentage": "x"}}, now)
	record(t, st, "work", obj{"five_hour": "junk"}, now)
	if got := report(t, st, now)[0].Pct; got != 19 {
		t.Errorf("pct = %d", got)
	}
	// A new window is sampled at once.
	record(t, st, "work", obj{"five_hour": win(1, now+6*h)}, now+10)
	st.QueryRow("SELECT count(*) FROM limit_samples").Scan(&samples)
	if samples != 2 {
		t.Errorf("%d samples after a reset", samples)
	}
}

func TestProfilesOnOneAccountAreShownOnce(t *testing.T) {
	st := open(t)
	record(t, st, "work", obj{"five_hour": win(40, now+h), "seven_day": win(47, now+86400)}, now-30)
	record(t, st, "personal", obj{"five_hour": win(41, now+h), "seven_day": win(47, now+86400)}, now)
	record(t, st, "other", obj{"five_hour": win(5, now+2*h)}, now) // another account: its window resets elsewhere
	type line struct {
		profile, label string
		pct            int
	}
	var got []line
	for _, e := range report(t, st, now) {
		got = append(got, line{e.Profile, e.Label, e.Pct})
	}
	want := []line{{"other", "5h", 5}, {"personal+work", "5h", 41}, {"personal+work", "7d", 47}}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("got %v, want %v", got, want)
	}
}
