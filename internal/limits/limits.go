// Package limits tracks the usage limits of each account: how much of the 5 hour and 7 day windows is
// used, and when it runs out.
//
// Claude Code only tells its status line about the limits, so the figures arrive through hc-status.sh.
package limits

import (
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/humblebundledore/headcoach/internal/store"
)

// window describes one kind of limit. Durations are in seconds.
type window struct {
	label    string
	length   float64 // length of the window
	lookback float64 // how far back the recent pace looks
	watched  float64 // how long the window must have been watched before that pace is trusted
}

var windows = map[string]window{
	"five_hour": {"5h", 5 * 3600, 30 * 60, 10 * 60},
	"seven_day": {"7d", 7 * 86400, 12 * 3600, 2 * 3600},
}

const (
	sampleEvery = 60
	staleAfter  = 900
	keepSamples = 8 * 86400
)

// Entry is one window of one account, as the board shows it.
type Entry struct {
	Profile  string `json:"profile"` // profiles that share the account, joined with +
	Kind     string `json:"kind"`
	Label    string `json:"label"`
	Pct      int    `json:"pct"`
	ResetsIn int    `json:"resets_in"`
	// LimitIn is the seconds until the limit is reached, only when that comes before the reset.
	LimitIn *int   `json:"limit_in"`
	Level   string `json:"level"` // ok, warm or hot
	Stale   bool   `json:"stale"`
}

// Record stores what one status line update says about the limits of a profile.
// rateLimits is the decoded `rate_limits` of the status line, of any shape.
func Record(st *store.Store, profile string, rateLimits any, now float64) error {
	limits, ok := rateLimits.(map[string]any)
	if !ok {
		return nil
	}
	for kind := range windows {
		entry, _ := limits[kind].(map[string]any)
		pct, ok1 := entry["used_percentage"].(float64)
		resetsAt, ok2 := entry["resets_at"].(float64)
		if !ok1 || !ok2 {
			continue
		}
		_, err := st.Exec(
			"INSERT INTO limits (profile, kind, pct, resets_at, at) VALUES (?, ?, ?, ?, ?)"+
				" ON CONFLICT (profile, kind) DO UPDATE SET pct = excluded.pct, resets_at = excluded.resets_at,"+
				" at = excluded.at",
			profile, kind, pct, resetsAt, now,
		)
		if err != nil {
			return err
		}
		// Keep one sample a minute, and always the first of a new window.
		var fresh int
		err = st.QueryRow(
			"SELECT count(*) FROM (SELECT at, resets_at FROM limit_samples WHERE profile = ? AND kind = ?"+
				" ORDER BY at DESC LIMIT 1) WHERE ? - at < ? AND resets_at = ?",
			profile, kind, now, sampleEvery, resetsAt,
		).Scan(&fresh)
		if err != nil {
			return err
		}
		if fresh > 0 {
			continue
		}
		_, err = st.Exec("INSERT INTO limit_samples (profile, kind, resets_at, at, pct) VALUES (?, ?, ?, ?, ?)",
			profile, kind, resetsAt, now, pct)
		if err != nil {
			return err
		}
		if _, err := st.Exec("DELETE FROM limit_samples WHERE at < ?", now-keepSamples); err != nil {
			return err
		}
	}
	return nil
}

type sample struct{ at, pct float64 }

// timeToLimit returns the seconds until the window is used up at the current pace.
// The second result is false when the pace is zero or unknown.
//
// The pace is the recent one when the window has been watched long enough, else its average since it opened.
func timeToLimit(kind string, pct, resetsAt float64, samples []sample, now float64) (float64, bool) {
	w := windows[kind]
	if pct >= 100 {
		return 0, true
	}
	var recent []sample
	for _, s := range samples {
		if now-w.lookback <= s.at && s.at <= now {
			recent = append(recent, s)
		}
	}
	rate := 0.0
	if len(recent) > 0 && now-recent[0].at >= w.watched {
		rate = (pct - recent[0].pct) / (now - recent[0].at)
	} else if elapsed := now - (resetsAt - w.length); elapsed >= w.lookback {
		// A window that has just opened says nothing about its pace yet.
		rate = pct / elapsed
	}
	if rate <= 0 {
		return 0, false
	}
	return (100 - pct) / rate, true
}

type row struct {
	profile, kind     string
	pct, resetsAt, at float64
	names             []string
}

// Report returns one entry per account and window that is still open, with a warning when it will run
// out before it resets.
func Report(st *store.Store, now float64) ([]Entry, error) {
	rows, err := st.Query("SELECT profile, kind, pct, resets_at, at FROM limits ORDER BY profile, kind")
	if err != nil {
		return nil, err
	}
	// Profiles that share one account report the same window: show it once, from the freshest report.
	type account struct {
		kind     string
		resetsAt int64
	}
	accounts := map[account]*row{}
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.profile, &r.kind, &r.pct, &r.resetsAt, &r.at); err != nil {
			rows.Close()
			return nil, err
		}
		if _, known := windows[r.kind]; !known || r.resetsAt <= now {
			continue // the window has rolled over: the old figure says nothing about the new one
		}
		key := account{r.kind, int64(r.resetsAt)}
		r.names = []string{r.profile}
		if seen := accounts[key]; seen != nil {
			r.names = append(seen.names, r.profile)
			if seen.at > r.at {
				seen.names = r.names
				continue
			}
		}
		accounts[key] = &r
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	merged := make([]*row, 0, len(accounts))
	for _, r := range accounts {
		sort.Strings(r.names)
		merged = append(merged, r)
	}
	sort.Slice(merged, func(i, j int) bool {
		if c := slices.Compare(merged[i].names, merged[j].names); c != 0 {
			return c < 0
		}
		return merged[i].kind < merged[j].kind
	})
	out := []Entry{}
	for _, r := range merged {
		samples, err := samplesOf(st, r)
		if err != nil {
			return nil, err
		}
		resetsIn := r.resetsAt - now
		pct := int(math.RoundToEven(r.pct))
		entry := Entry{
			Profile: strings.Join(r.names, "+"), Kind: r.kind, Label: windows[r.kind].label, Pct: pct,
			ResetsIn: int(resetsIn), Level: "ok", Stale: now-r.at > staleAfter,
		}
		left, known := timeToLimit(r.kind, r.pct, r.resetsAt, samples, now)
		runsOut := known && left < resetsIn
		if runsOut {
			seconds := int(left)
			entry.LimitIn = &seconds
		}
		switch {
		case runsOut || pct >= 85:
			entry.Level = "hot"
		case pct >= 60:
			entry.Level = "warm"
		}
		out = append(out, entry)
	}
	return out, nil
}

func samplesOf(st *store.Store, r *row) ([]sample, error) {
	rows, err := st.Query(
		"SELECT at, pct FROM limit_samples WHERE profile = ? AND kind = ? AND resets_at = ? ORDER BY at",
		r.profile, r.kind, r.resetsAt,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sample
	for rows.Next() {
		var s sample
		if err := rows.Scan(&s.at, &s.pct); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
