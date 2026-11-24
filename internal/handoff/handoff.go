// Package handoff reads the handoff file of a task: what an agent writes down so that the work can go
// on from a fresh context, and so that the task driving it can tell where it stands.
//
// The file is Markdown. Its sections are read by their heading, in any order, and any of them may be
// missing; other sections are kept as they are, for the people reading the file:
//
//	## Status   where the work stands, in a line or two
//	## Running  what runs now: a session, a job, a pull request, a deploy
//	## Waiting  what waits on whom, one "- on <who>: <what>" line each
//	## Next     the next step
//	## Queue    what comes after: "- [ ] item", and "- [x] item" once done
//
// <who> is "you", a task id, or anything else such as "ci". HTML comments are guidance, never content.
package handoff

import (
	"regexp"
	"strings"
)

// Template is the file hc handoff init writes: the sections, with guidance only.
const Template = `# Handoff

## Status
<!-- where the work stands, in a line or two -->

## Running
<!-- - what runs now: a session, a job, a pull request, a deploy -->

## Waiting
<!-- - on you: what waits on your user; - on <task id>: on another task; - on ci: on anything else -->

## Next
<!-- - the next step, enough to pick the work up from a fresh context -->

## Queue
<!-- - [ ] what comes after; - [x] once it is done -->
`

// Wait is one thing that waits on someone.
type Wait struct {
	On   string `json:"on"` // you, a task id, or anything else
	What string `json:"what"`
}

// Queue counts the items of the queue, and names the first one still open.
type Queue struct {
	Open int    `json:"open"`
	Done int    `json:"done"`
	Next string `json:"next"`
}

// Summary is what the board reads from a handoff file.
type Summary struct {
	Status  string   `json:"status"`
	Running []string `json:"running"`
	Waiting []Wait   `json:"waiting"`
	Next    []string `json:"next"`
	Queue   Queue    `json:"queue"`
}

var (
	commentRe = regexp.MustCompile(`(?s)<!--.*?-->`)
	headingRe = regexp.MustCompile(`^#{2,3}\s+(.+?)\s*#*$`)
	bulletRe  = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s+(.*)$`)
	boxRe     = regexp.MustCompile(`^\[([ xX])\]\s*(.*)$`)
	onRe      = regexp.MustCompile(`(?i)^[*_]*(?:waiting\s+)?on\s+([^:*_]+?)[*_]*\s*:[*_]*\s*(.+)$`)
)

// Parse reads a handoff file. It never fails: what it does not recognise, it leaves out.
func Parse(text string) Summary {
	out := Summary{Running: []string{}, Waiting: []Wait{}, Next: []string{}}
	sections := map[string][]string{}
	section := ""
	for _, line := range strings.Split(commentRe.ReplaceAllString(text, ""), "\n") {
		if found := headingRe.FindStringSubmatch(strings.TrimSpace(line)); found != nil {
			section = strings.ToLower(strings.Trim(found[1], "*_` :"))
			continue
		}
		if strings.TrimSpace(line) != "" {
			sections[section] = append(sections[section], line)
		}
	}
	out.Status = oneLine(strings.Join(sections["status"], " "))
	out.Running = items(sections["running"])
	out.Next = items(sections["next"])
	for _, item := range items(sections["waiting"]) {
		if found := onRe.FindStringSubmatch(item); found != nil {
			out.Waiting = append(out.Waiting, Wait{On: strings.Trim(found[1], "`"), What: found[2]})
		} else {
			out.Waiting = append(out.Waiting, Wait{What: item})
		}
	}
	for _, item := range items(sections["queue"]) {
		found := boxRe.FindStringSubmatch(item)
		switch {
		case found == nil || found[1] == " ":
			out.Queue.Open++
			if out.Queue.Next == "" {
				out.Queue.Next = item
				if found != nil {
					out.Queue.Next = found[2]
				}
			}
		default:
			out.Queue.Done++
		}
	}
	return out
}

// items reads the bullets of a section, one item per bullet; lines under a bullet go on it. Text that
// is not a bullet counts as one item of its own.
func items(lines []string) []string {
	out := []string{}
	for _, line := range lines {
		if found := bulletRe.FindStringSubmatch(line); found != nil {
			out = append(out, oneLine(found[1]))
		} else if len(out) > 0 && strings.HasPrefix(line, " ") {
			out[len(out)-1] = oneLine(out[len(out)-1] + " " + line)
		} else {
			out = append(out, oneLine(line))
		}
	}
	return out
}

func oneLine(text string) string { return strings.Join(strings.Fields(text), " ") }

// OnYou lists what waits on you.
func (s Summary) OnYou() []string {
	var out []string
	for _, w := range s.Waiting {
		if strings.EqualFold(w.On, "you") || strings.EqualFold(w.On, "me") || strings.EqualFold(w.On, "user") {
			out = append(out, w.What)
		}
	}
	return out
}
