package tui

import (
	"math"
	"strings"
	"unicode"
)

// The weights of a fuzzy match: each letter found scores, more when it follows the letter found
// before it or starts a word, and each letter skipped between two found ones costs a little.
const (
	fuzzyHit   = 16
	fuzzyRun   = 8
	fuzzyStart = 8
	fuzzyGap   = 1
)

// fuzzy scores a filter against the fields of a task. Each word of the filter must be found in one
// field, its letters in order but not always side by side, case ignored: "hcfz" finds "headcoach
// fuzzy". ok is false when a word is found in no field. An empty filter fits with a score of 0.
func fuzzy(filter string, fields ...string) (score int, ok bool) {
	for _, word := range strings.Fields(strings.ToLower(filter)) {
		best, found := 0, false
		for _, field := range fields {
			if s, hit := fuzzyWord([]rune(word), []rune(field)); hit && (!found || s > best) {
				best, found = s, true
			}
		}
		if !found {
			return 0, false
		}
		score += best
	}
	return score, true
}

// fuzzyWord finds the letters of a lower case word in text, in order, at the places that score best.
func fuzzyWord(word, text []rune) (int, bool) {
	if len(word) == 0 || len(word) > len(text) {
		return 0, len(word) == 0
	}
	const none = math.MinInt / 2   // the letter cannot end here
	prev := make([]int, len(text)) // the best score of the word so far, its last letter at each place
	cur := make([]int, len(text))
	for i, w := range word {
		gap := none // the best score of the word so far ending two places back or more, less the letters skipped
		for j, r := range text {
			if j >= 2 {
				gap = max(gap, prev[j-2]) - fuzzyGap
			}
			cur[j] = none
			if unicode.ToLower(r) != w {
				continue
			}
			bonus := fuzzyHit
			if wordStart(text, j) {
				bonus += fuzzyStart
			}
			switch {
			case i == 0:
				cur[j] = bonus
			case j > 0:
				cur[j] = bonus + max(prev[j-1]+fuzzyRun, gap)
			}
		}
		prev, cur = cur, prev
	}
	best := none
	for _, s := range prev {
		best = max(best, s)
	}
	return best, best > none/2
}

// wordStart says whether the letter at i starts a word: the first one, one after a space or a
// sign, or a capital after a small letter.
func wordStart(text []rune, i int) bool {
	if i == 0 {
		return true
	}
	before := text[i-1]
	return !unicode.IsLetter(before) && !unicode.IsDigit(before) ||
		unicode.IsLower(before) && unicode.IsUpper(text[i])
}
