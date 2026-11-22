package transcript

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type obj = map[string]any

// write stores entries one per line. A string is written as it is, anything else as JSON.
func write(t *testing.T, entries ...any) string {
	t.Helper()
	var sb strings.Builder
	for _, entry := range entries {
		if s, ok := entry.(string); ok {
			sb.WriteString(s)
		} else {
			raw, _ := json.Marshal(entry)
			sb.Write(raw)
		}
		sb.WriteString("\n")
	}
	path := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func user(content any) obj {
	return obj{"type": "user", "message": obj{"role": "user", "content": content}}
}

func assistant(blocks ...any) obj {
	return obj{"type": "assistant", "message": obj{"role": "assistant", "content": blocks}}
}

func text(value any) obj { return obj{"type": "text", "text": value} }

func with(entry obj, key string) obj {
	entry[key] = true
	return entry
}

// parse reads the log of one transcript.
func parse(path string, limit int) []Turn { return Log([]string{path}, limit) }

func check(t *testing.T, got []Turn, want ...Turn) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestMissingAndEmptyFiles(t *testing.T) {
	tmp := t.TempDir()
	check(t, parse("", 80))
	check(t, parse(filepath.Join(tmp, "nope.jsonl"), 80))
	check(t, parse(tmp, 80))
	check(t, parse(write(t), 80))
	check(t, parse(write(t, user("a"), user("b")), 0))
}

func TestUserContent(t *testing.T) {
	path := write(t,
		user("  fix the bug  "),
		user([]any{text("first part"), obj{"type": "image", "source": obj{}}, text("second part")}),
		user([]any{obj{"type": "tool_result", "tool_use_id": "t1", "content": "tool output"}}),
	)
	check(t, parse(path, 80), Turn{Who: "you", Text: "fix the bug"}, Turn{Who: "you", Text: "first part\nsecond part"})
}

func TestMessageTypedWhileTheAgentWorks(t *testing.T) {
	queued := func(mode string, prompt any) obj {
		return obj{"type": "attachment", "attachment": obj{"type": "queued_command", "commandMode": mode, "prompt": prompt}}
	}
	// set gives the attachment of an entry one more field.
	set := func(entry obj, key string, value any) obj {
		entry["attachment"].(obj)[key] = value
		return entry
	}
	path := write(t,
		user("fix the bug"),
		assistant(text("On it.")),
		queued("prompt", "  and the docs  "),
		queued("prompt", []any{text("with a picture"), obj{"type": "image", "source": obj{}}}),
		queued("task-notification", "<task-notification>agent done</task-notification>"),
		queued("prompt", "<task-notification>agent done</task-notification>"),
		queued("prompt", "  "),
		queued("prompt", 42),
		set(queued("prompt", "typed, and marked so"), "origin", obj{"kind": "human"}),
		// The report of a sub-agent is queued as a prompt too.
		set(set(queued("prompt", "<agent-message from=\"reviewer\">found 3</agent-message>"), "origin", obj{"kind": "peer"}), "isMeta", true),
		set(queued("prompt", "from another session"), "origin", obj{"kind": "peer"}),
		set(queued("prompt", "written by the harness"), "isMeta", true),
		obj{"type": "attachment", "isSidechain": true, "attachment": obj{"type": "queued_command", "commandMode": "prompt", "prompt": "typed inside a sub-agent"}},
		obj{"type": "attachment", "attachment": obj{"type": "deferred_tools_record", "commandMode": "prompt", "prompt": "x"}},
		obj{"type": "attachment", "attachment": "a string"},
		obj{"type": "attachment"},
	)
	check(t, parse(path, 80), Turn{Who: "you", Text: "fix the bug"}, Turn{Who: "agent", Text: "On it."},
		Turn{Who: "you", Text: "and the docs"}, Turn{Who: "you", Text: "with a picture"}, Turn{Who: "you", Text: "typed, and marked so"})
}

func TestSlashCommand(t *testing.T) {
	path := write(t,
		user("<command-name>/clear</command-name>\n<command-message>clear</command-message>\n<command-args></command-args>"),
		user("<command-message>deploy</command-message>\n<command-name>/deploy</command-name>\n<command-args>fix the log\nand the docs</command-args>"),
		user([]any{text("<command-message>deploy</command-message>\n<command-name>/deploy</command-name>")}),
		user("see <command-name>/deploy</command-name> in the docs"),
	)
	check(t, parse(path, 80), Turn{Who: "you", Text: "/clear"}, Turn{Who: "you", Text: "/deploy fix the log\nand the docs"},
		Turn{Who: "you", Text: "/deploy"}, Turn{Who: "you", Text: "see <command-name>/deploy</command-name> in the docs"})
	// A slash command is your last prompt as much as a sentence is.
	entries, work := tools(0, 100)
	entries = append([]any{user("fix A"), user("<command-message>deploy</command-message>\n<command-name>/deploy</command-name>\n<command-args>fix B</command-args>")}, entries...)
	check(t, parse(write(t, entries...), 80), join([]Turn{{Who: "you", Text: "/deploy fix B", Skipped: 20}}, work[20:])...)
}

func TestAssistantTextAndToolUse(t *testing.T) {
	path := write(t, assistant(
		obj{"type": "thinking", "thinking": "hmm"},
		text("I will look."),
		obj{"type": "tool_use", "id": "t1", "name": "Bash", "input": obj{"command": "ls -la"}},
		obj{"type": "tool_use", "id": "t2", "name": "Edit", "input": obj{"file_path": "/a/b.py"}},
		obj{"type": "tool_use", "id": "t3", "name": "mcp__srv__do", "input": nil},
		text("   "),
		text(nil),
	))
	check(t, parse(path, 80),
		Turn{Who: "agent", Text: "I will look."}, Turn{Who: "tool", Text: "$ ls -la"},
		Turn{Who: "tool", Text: "Edit b.py"}, Turn{Who: "tool", Text: "srv: do"})
}

func TestSkippedEntries(t *testing.T) {
	entries := []any{
		with(user("sub-agent prompt"), "isSidechain"),
		with(assistant(text("sub-agent answer")), "isSidechain"),
		with(user("meta text"), "isMeta"),
		with(user("This session is being continued from a previous conversation."), "isCompactSummary"),
		"{not json", "", "[1, 2, 3]", `"a string"`, "null", "\xff\xfe broken",
		obj{"type": "summary", "summary": "x"},
		obj{"type": "user"},
		obj{"type": "user", "message": "not a dict"},
		obj{"type": "user", "message": obj{"content": nil}},
		obj{"type": "user", "message": obj{"content": 42}},
		obj{"type": "assistant", "message": obj{"content": "a plain string"}},
		obj{"type": "assistant", "message": obj{"content": []any{"str block", 7, nil}}},
		obj{"type": "system", "message": obj{"content": "sys"}},
		assistant(obj{"type": "tool_use", "name": "Edit", "input": obj{"file_path": 42}}),
	}
	for _, noise := range []string{
		"<command-stdout>ok</command-stdout>", "<local-command-stdout>ok</local-command-stdout>",
		"<system-reminder>be nice</system-reminder>", "<task-notification>agent done</task-notification>",
		"Caveat: The messages below were generated by the user", "  \n<command-message>x</command-message>",
		"[Request interrupted by user]", "[Request interrupted by user for tool use]",
	} {
		entries = append(entries, user(noise), user([]any{text(noise)}))
	}
	entries = append(entries, user("survivor"))
	check(t, parse(write(t, entries...), 80), Turn{Who: "tool", Text: "Edit"}, Turn{Who: "you", Text: "survivor"})
}

func TestLimitAndClipping(t *testing.T) {
	var entries []any
	for i := 0; i < 200; i++ {
		entries = append(entries, user(fmt.Sprintf("msg %d", i)))
	}
	path := write(t, entries...)
	check(t, parse(path, 3), Turn{Who: "you", Text: "msg 197"}, Turn{Who: "you", Text: "msg 198"}, Turn{Who: "you", Text: "msg 199"})
	if got := parse(path, 80); len(got) != 80 || got[0].Text != "msg 120" {
		t.Errorf("default limit gave %d turns from %q", len(got), got[0].Text)
	}
	for _, turn := range parse(write(t, user(strings.Repeat("u", 10_000)), assistant(text(strings.Repeat("a", 10_000)))), 80) {
		if len(turn.Text) != textLimit+len("\n…") || !strings.HasSuffix(turn.Text, "\n…") {
			t.Errorf("a long %s turn is %d bytes", turn.Who, len(turn.Text))
		}
	}
}

func TestReadsBackAsFarAsTheTurnsAsked(t *testing.T) {
	pad := strings.Repeat("x", 300)
	var entries []any
	for i := 0; i < 4000; i++ {
		entries = append(entries, user(fmt.Sprintf("msg %05d %s", i, pad)))
	}
	path := write(t, entries...) // longer than one chunk, so lines are cut between two reads
	whole := func(turns []Turn, n int) {
		t.Helper()
		if len(turns) != n {
			t.Fatalf("%d turns, want %d", len(turns), n)
		}
		for i, turn := range turns {
			if want := fmt.Sprintf("msg %05d %s", 4000-n+i, pad); turn.Text != want {
				t.Fatalf("turn %d = %.20q", i, turn.Text)
			}
		}
	}
	whole(parse(path, 10_000), 4000)
	whole(parse(path, 3500), 3500)
	// A line longer than a chunk is read whole too.
	long := parse(write(t, user("small"), user(strings.Repeat("y", chunkBytes+1000)), assistant(text("ok"))), 80)
	if len(long) != 3 || long[0].Text != "small" || !strings.HasPrefix(long[1].Text, "yyy") {
		t.Errorf("around a long line: %d turns", len(long))
	}

	// The budget stops a read that finds no end: what it got is whole, and the newest.
	defer func(n int64) { scanBytes = n }(scanBytes)
	scanBytes = 500_000
	if turns := parse(path, 10_000); len(turns) < 1000 || len(turns) >= 2000 {
		t.Errorf("%d turns within the budget", len(turns))
	} else {
		whole(turns, len(turns))
	}
}

// tools is n tool turns of the agent, numbered from `from`.
func tools(from, n int) (entries []any, turns []Turn) {
	for i := from; i < from+n; i++ {
		command := fmt.Sprintf("step %d", i)
		entries = append(entries, assistant(obj{"type": "tool_use", "name": "Bash", "input": obj{"command": command}}))
		turns = append(turns, Turn{Who: "tool", Text: "$ " + command})
	}
	return entries, turns
}

// join puts runs of turns end to end, in a slice of its own.
func join(runs ...[]Turn) (out []Turn) {
	for _, run := range runs {
		out = append(out, run...)
	}
	return out
}

func TestWindow(t *testing.T) {
	_, work := tools(0, 10)
	prompt := func(skipped int) []Turn { return []Turn{{Who: "you", Text: "do it", Skipped: skipped}} }
	log := join([]Turn{{Who: "agent", Text: "hi"}}, prompt(0), []Turn{{Who: "agent", Text: "ok"}}, work)
	check(t, Window(log, 0))
	check(t, Window(log, 13), log...)
	check(t, Window(log, 12), log[1:]...)
	// Your last prompt is older than the window: it comes first, with what is left out after it.
	check(t, Window(log, 4), join(prompt(7), work[6:])...)
	check(t, Window(log, 10), join(prompt(1), work)...)
	// A window of a window adds up.
	check(t, Window(Window(log, 4), 2), join(prompt(9), work[8:])...)
	check(t, Window(work, 3), work[7:]...)
	for skipped, want := range map[int]string{0: "", 1: "… 1 turn …", 7: "… 7 turns …"} {
		if got := (Turn{Skipped: skipped}).Gap(); got != want {
			t.Errorf("gap of %d = %q", skipped, got)
		}
	}
}

func TestPromptOlderThanTheLog(t *testing.T) {
	entries, work := tools(0, 100)
	// The shape of a long turn: megabytes of tool output after the prompt.
	entries = append([]any{user("first"), assistant(text("done")), user("the last prompt")}, entries...)
	entries = append(entries, user([]any{obj{"type": "tool_result", "content": strings.Repeat("z", 2*chunkBytes)}}))
	check(t, parse(write(t, entries...), 80), join([]Turn{{Who: "you", Text: "the last prompt", Skipped: 20}}, work[20:])...)
}

func TestLogAcrossSessions(t *testing.T) {
	oldEntries, oldWork := tools(0, 30)
	newEntries, newWork := tools(30, 30)
	first := write(t, append([]any{user("start")}, oldEntries...)...)
	second := write(t, newEntries...)
	missing := filepath.Join(t.TempDir(), "gone.jsonl")
	start := func(skipped int) []Turn { return []Turn{{Who: "you", Text: "start", Skipped: skipped}} }
	// The newest session comes last, and an older one only fills what is missing.
	check(t, Log([]string{first, missing, second}, 80), join(start(0), oldWork, newWork)...)
	check(t, Log([]string{first, second}, 40), join(start(20), oldWork[20:], newWork)...)
	check(t, Log([]string{second, first}, 40), join(newWork[21:], start(0), oldWork)...)
	check(t, Log([]string{second, first}, 31), join(start(0), oldWork)...)
	check(t, Log([]string{first, second}, 0))
	check(t, Log(nil, 80))
}

func TestRender(t *testing.T) {
	html := Render("## Thread 1\n\n- **bold** and `code`\n\n> a quote\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n~~gone~~\n")
	for _, piece := range []string{"<h2>Thread 1</h2>", "<strong>bold</strong>", "<code>code</code>", "<blockquote>", "<table>", "<del>gone</del>"} {
		if !strings.Contains(html, piece) {
			t.Errorf("%s is missing from %s", piece, html)
		}
	}
	// Raw HTML is shown as text, inline and as a block.
	for _, source := range []string{
		"hello <script>alert(1)</script> <img src=x onerror=alert(1)>",
		"<script>alert(1)</script>\n\n<div onclick=x>\nblock\n</div>\n",
	} {
		html = Render(source)
		if strings.Contains(html, "<script") || strings.Contains(html, "<img") || strings.Contains(html, "<div") ||
			!strings.Contains(html, "&lt;script&gt;") {
			t.Errorf("raw HTML came through: %s", html)
		}
	}
	html = Render("[click](javascript:alert(1)) and [ok](https://example.com)")
	if strings.Contains(html, `href="javascript:`) || !strings.Contains(html, `href="https://example.com"`) {
		t.Errorf("links: %s", html)
	}
}
