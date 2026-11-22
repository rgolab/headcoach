// Package sh quotes words for /bin/sh.
package sh

import (
	"regexp"
	"strings"
)

var safe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// Quote returns a word that the shell reads back as s, whatever s holds.
func Quote(s string) string {
	if safe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
