package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// searchDays is how far Latest and Next look for a time a cron line matches.
const searchDays = 5 * 366

// cron is a parsed cron line: the values each of its five fields allows.
type cron struct {
	minute, hour, dom, month, dow [60]bool
	// domAny and dowAny are set when the field starts with *. A day must then match both fields;
	// when neither starts with *, a day matching either one is enough, as in cron.
	domAny, dowAny bool
}

type cronField struct {
	name     string
	min, max int
	names    []string // names for the values from min, such as jan or sun
}

var cronFields = []cronField{
	{"minute", 0, 59, nil},
	{"hour", 0, 23, nil},
	{"day of month", 1, 31, nil},
	{"month", 1, 12, []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}},
	{"day of week", 0, 7, dayNames},
}

// IsCron tells whether a schedule's time is a cron line rather than a time of day.
func IsCron(at string) bool { return strings.ContainsAny(strings.TrimSpace(at), " \t") }

// parseCron reads a cron line of five fields: minute, hour, day of month, month, day of week.
// A field is *, a value, a range a-b, a list a,b, each with an optional step such as */15.
func parseCron(line string) (*cron, error) {
	words := strings.Fields(line)
	if len(words) != 5 {
		return nil, fmt.Errorf("'%s' is not a cron line of five fields such as */15 * * * *", line)
	}
	// A day field starting with *, such as */2, counts as unrestricted, as in cron.
	c := &cron{domAny: strings.HasPrefix(words[2], "*"), dowAny: strings.HasPrefix(words[4], "*")}
	sets := []*[60]bool{&c.minute, &c.hour, &c.dom, &c.month, &c.dow}
	for i, word := range words {
		if err := cronFields[i].parse(strings.ToLower(word), sets[i]); err != nil {
			return nil, fmt.Errorf("'%s' in the cron line: %v", word, err)
		}
	}
	if c.dow[7] {
		c.dow[0] = true
	}
	return c, nil
}

func (f cronField) parse(word string, set *[60]bool) error {
	for _, part := range strings.Split(word, ",") {
		span, stepText, stepped := strings.Cut(part, "/")
		step := 1
		if stepped {
			n, err := strconv.Atoi(stepText)
			if err != nil || n < 1 {
				return fmt.Errorf("the step '%s' is not a number above 0", stepText)
			}
			step = n
		}
		lo, hi := f.min, f.max
		if span != "*" {
			from, to, ranged := strings.Cut(span, "-")
			var err error
			if lo, err = f.value(from); err != nil {
				return err
			}
			hi = lo
			if ranged {
				if hi, err = f.value(to); err != nil {
					return err
				}
			} else if stepped {
				hi = f.max
			}
			if hi < lo {
				return fmt.Errorf("the range %s ends before it starts", span)
			}
		}
		for v := lo; v <= hi; v += step {
			set[v] = true
		}
	}
	return nil
}

func (f cronField) value(word string) (int, error) {
	for i, name := range f.names {
		if word == name {
			return f.min + i, nil
		}
	}
	n, err := strconv.Atoi(word)
	if err != nil || n < f.min || n > f.max {
		return 0, fmt.Errorf("the %s must be from %d to %d", f.name, f.min, f.max)
	}
	return n, nil
}

func (c *cron) day(t time.Time) bool {
	if !c.month[t.Month()] {
		return false
	}
	dom, dow := c.dom[t.Day()], c.dow[t.Weekday()]
	if c.domAny || c.dowAny {
		return dom && dow
	}
	return dom || dow
}

// latest is the last minute the cron line matches, at or before now. It skips a day or an hour
// at a time when they do not match.
func (c *cron) latest(now time.Time) (time.Time, bool) {
	t := now.Truncate(time.Minute)
	limit := now.AddDate(0, 0, -searchDays)
	for t.After(limit) {
		switch {
		case !c.day(t):
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()).Add(-time.Minute)
		case !c.hour[t.Hour()]:
			t = t.Add(-time.Duration(t.Minute()+1) * time.Minute)
		case !c.minute[t.Minute()]:
			t = t.Add(-time.Minute)
		default:
			return t, true
		}
	}
	return time.Time{}, false
}

// next is the first minute the cron line matches, after now.
func (c *cron) next(now time.Time) (time.Time, bool) {
	t := now.Truncate(time.Minute).Add(time.Minute)
	limit := now.AddDate(0, 0, searchDays)
	for t.Before(limit) {
		switch {
		case !c.day(t):
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
		case !c.hour[t.Hour()]:
			t = t.Add(time.Duration(60-t.Minute()) * time.Minute)
		case !c.minute[t.Minute()]:
			t = t.Add(time.Minute)
		default:
			return t, true
		}
	}
	return time.Time{}, false
}
