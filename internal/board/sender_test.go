package board

import "testing"

func TestLabel(t *testing.T) {
	for _, c := range []struct {
		sender, task string
		coordinator  bool
		want         string
	}{
		{FromYou, "", false, ""},
		{FromTask, "1a2b3c4d-0000-4000-8000-000000000001", false, "[from task 1a2b3c4d] "},
		{FromTask, "1a2b3c4d-0000-4000-8000-000000000001", true, "[from coordinator 1a2b3c4d] "},
		{FromAgent, "", false, "[from an agent] "},
		{FromHC, "", false, "[from hc] "},
	} {
		if got := Label(c.sender, c.task, c.coordinator); got != c.want {
			t.Errorf("Label(%q, %q, %v) = %q, want %q", c.sender, c.task, c.coordinator, got, c.want)
		}
	}
}

func TestLabeledAndSplitLabel(t *testing.T) {
	const label = "[from task 1a2b3c4d] "
	for _, c := range []struct{ label, text, want, from, rest string }{
		{"", "stop", "stop", "", "stop"},
		{label, "stop\nnow", "[from task 1a2b3c4d] stop\nnow", "task 1a2b3c4d", "stop\nnow"},
		// A slash command keeps its place: the label goes before its arguments.
		{label, "/reviewer the branch", "/reviewer [from task 1a2b3c4d] the branch", "task 1a2b3c4d", "/reviewer the branch"},
		// A bare command is an action: nothing to label.
		{label, "/compact", "/compact", "", "/compact"},
		{label, "/compact  ", "/compact  ", "", "/compact  "},
		{"[from hc] ", "2 child updates:\n- one", "[from hc] 2 child updates:\n- one", "hc", "2 child updates:\n- one"},
		{"[from an agent] ", "hi", "[from an agent] hi", "an agent", "hi"},
	} {
		got := Labeled(c.label, c.text)
		if got != c.want {
			t.Errorf("Labeled(%q, %q) = %q, want %q", c.label, c.text, got, c.want)
		}
		if from, rest := SplitLabel(got); from != c.from || rest != c.rest {
			t.Errorf("SplitLabel(%q) = %q, %q, want %q, %q", got, from, rest, c.from, c.rest)
		}
	}
	// Words that only look like a label somewhere else are not one.
	for _, text := range []string{"see [from task 1a2b3c4d] above", "[from you] hi", "[from task] hi", "[from task 123456789] hi"} {
		if from, _ := SplitLabel(text); from != "" {
			t.Errorf("SplitLabel(%q) found %q", text, from)
		}
	}
}

func TestSaid(t *testing.T) {
	for text, want := range map[string]string{
		"  fix it\nplease":                          "You: fix it",
		"[from coordinator 1a2b3c4d] stop\nnow":     "From coordinator 1a2b3c4d: stop",
		"/reviewer [from task 1a2b3c4d] the branch": "From task 1a2b3c4d: /reviewer the branch",
		"[from hc] child updates":                   "From hc: child updates",
	} {
		if got := Said(text); got != want {
			t.Errorf("Said(%q) = %q, want %q", text, got, want)
		}
	}
}
