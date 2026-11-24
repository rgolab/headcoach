package tui

import "testing"

func TestFuzzyFindsWordsInAnyField(t *testing.T) {
	fields := []string{"Fix the fuzzy filter", "headcoach", "a1b2c3d4"}
	for _, filter := range []string{"", "  ", "fuzzy", "FUZZ", "ffr", "hcoach", "a1b2", "abcd", "fuzzy hc", "hc fuzzy"} {
		if _, ok := fuzzy(filter, fields...); !ok {
			t.Errorf("%q does not fit %q", filter, fields)
		}
	}
	// A word is found in one field, its letters in order.
	for _, filter := range []string{"zuff", "filterhead", "fuzzy nope", "fix headcoachx"} {
		if _, ok := fuzzy(filter, fields...); ok {
			t.Errorf("%q fits %q", filter, fields)
		}
	}
}

func TestFuzzyScoresRunsAndWordStartsHigher(t *testing.T) {
	score := func(filter, text string) int {
		t.Helper()
		s, ok := fuzzy(filter, text)
		if !ok {
			t.Fatalf("%q does not fit %q", filter, text)
		}
		return s
	}
	for _, c := range []struct{ filter, better, worse string }{
		{"filter", "the filter view", "fix it later, tell her"}, // side by side beats scattered
		{"fz", "fuzzy zone", "fuzzy"},                           // two word starts beat one
		{"tui", "tui: add keys", "statusbuild"},                 // a whole word beats the letters inside others
		{"rs", "reloadState", "reloads"},                        // a capital starts a word
		{"ab", "ab", "a-------b"},                               // a short gap beats a long one
	} {
		if b, w := score(c.filter, c.better), score(c.filter, c.worse); b <= w {
			t.Errorf("%q scores %d on %q, not more than %d on %q", c.filter, b, c.better, w, c.worse)
		}
	}
}
