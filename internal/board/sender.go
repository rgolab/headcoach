package board

import (
	"regexp"
	"strings"
	"unicode"
)

// The senders of a message the board types into a session.
const (
	FromYou   = "you"   // you, at a terminal or on a board
	FromTask  = "task"  // another task's session
	FromAgent = "agent" // an agent the board cannot tie to a task
	FromHC    = "hc"    // the board itself
)

// labelRe finds the label of a message another agent sent, after the slash command it may follow.
var labelRe = regexp.MustCompile(`^(/\S+\s+)?\[from ((?:coordinator|task) [A-Za-z0-9-]{1,8}|an agent|hc)\]\s*`)

// Label is what goes before a message so that a session can tell its user's words from another
// agent's. Your own messages carry none. coordinator says the sending task is the recipient's parent.
func Label(sender, senderTask string, coordinator bool) string {
	switch sender {
	case FromTask:
		role := "task"
		if coordinator {
			role = "coordinator"
		}
		return "[from " + role + " " + Short(senderTask) + "] "
	case FromAgent:
		return "[from an agent] "
	case FromHC:
		return "[from hc] "
	}
	return ""
}

// Labeled puts a label before a message. A slash command keeps its place, the label goes before its
// arguments; a bare command is an action with no words to label.
func Labeled(label, text string) string {
	if label == "" || !strings.HasPrefix(text, "/") {
		return label + text
	}
	i := strings.IndexFunc(text, unicode.IsSpace)
	if i < 0 || strings.TrimSpace(text[i:]) == "" {
		return text
	}
	return text[:i] + " " + label + strings.TrimSpace(text[i:])
}

// SplitLabel returns who the label of a message names, such as "coordinator 1a2b3c4d", and the
// message without it. from is empty when the message has no label.
func SplitLabel(text string) (from, rest string) {
	found := labelRe.FindStringSubmatchIndex(text)
	if found == nil {
		return "", text
	}
	command := ""
	if found[2] >= 0 {
		command = text[found[2]:found[3]]
	}
	return text[found[4]:found[5]], command + text[found[1]:]
}

// Said is the line the board shows for a message typed into a session: who said it, then its first line.
func Said(text string) string {
	if from, rest := SplitLabel(text); from != "" {
		return Clip("From "+from+": "+FirstLine(rest), 100)
	}
	return Clip("You: "+FirstLine(text), 100)
}
