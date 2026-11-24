package handoff

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	got := Parse(`# Handoff

## Status
Fixing the login bug: the cause is found,
the fix is on the branch.

## Running
- the test suite, in the background
- PR #12, its CI
  still pending

## Waiting
- on you: the go to merge PR #12
- On 1a2b3c4d: its review
- **on ci**: the release build
- the weather

## Next
1. merge once green
2. close the issue

## Notes
Anything else is kept for the people reading the file.

### Queue
- [x] reproduce the bug
- [ ] write the fix <!-- done soon -->
- [ ] update the docs
- [X] add a test
- a line without a box is open
`)
	want := Summary{
		Status:  "Fixing the login bug: the cause is found, the fix is on the branch.",
		Running: []string{"the test suite, in the background", "PR #12, its CI still pending"},
		Waiting: []Wait{{"you", "the go to merge PR #12"}, {"1a2b3c4d", "its review"}, {"ci", "the release build"}, {"", "the weather"}},
		Next:    []string{"merge once green", "close the issue"},
		Queue:   Queue{Open: 3, Done: 2, Next: "write the fix"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Parse =\n%+v\nwant\n%+v", got, want)
	}
	if onYou := got.OnYou(); !reflect.DeepEqual(onYou, []string{"the go to merge PR #12"}) {
		t.Errorf("OnYou = %q", onYou)
	}
}

// The template is guidance only: an agent that fills nothing in says nothing.
func TestTemplateSaysNothing(t *testing.T) {
	want := Summary{Running: []string{}, Waiting: []Wait{}, Next: []string{}}
	for _, text := range []string{Template, "", "no sections at all"} {
		if got := Parse(text); !reflect.DeepEqual(got, want) {
			t.Errorf("Parse(%q) = %+v", text, got)
		}
	}
}
