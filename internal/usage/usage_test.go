package usage

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func usageLine(id string, in, out, read, write int) string {
	return fmt.Sprintf(`{"type": "assistant", "message": {"id": %q, "usage": {"input_tokens": %d, "output_tokens": %d,`+
		` "cache_read_input_tokens": %d, "cache_creation_input_tokens": %d}}}`+"\n", id, in, out, read, write)
}

func appendTo(t *testing.T, path, text string) {
	t.Helper()
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	fh.WriteString(text)
}

func TestTokensOfASessionAndItsSubAgents(t *testing.T) {
	tmp := t.TempDir()
	main := filepath.Join(tmp, "s1.jsonl")
	// One answer is written twice with a growing output count: the last line of a message id wins.
	appendTo(t, main, usageLine("m1", 10, 5, 100, 0)+usageLine("m1", 10, 9, 100, 0)+usageLine("m2", 1, 2, 0, 50))
	sub := filepath.Join(tmp, "s1", "subagents")
	os.MkdirAll(sub, 0o755)
	appendTo(t, filepath.Join(sub, "agent-a1.jsonl"), usageLine("x1", 1000, 200, 3000, 0))
	appendTo(t, filepath.Join(sub, "notes.txt"), "not a transcript")

	own, byAgent := OfSession(main)
	if want := (Tokens{11, 11, 100, 50}); own != want {
		t.Errorf("own = %+v, want %+v", own, want)
	}
	if want := (Tokens{1000, 200, 3000, 0}); len(byAgent) != 1 || byAgent["a1"] != want {
		t.Errorf("sub-agents = %+v", byAgent)
	}
	if got := own.Add(byAgent["a1"]).Total(); got != 4372 {
		t.Errorf("total = %d", got)
	}

	// Only what was appended is read, and a line still being written waits.
	appendTo(t, main, usageLine("m3", 5, 5, 0, 0)+`{"type": "assistant", "message": {"id": "m4", "usa`)
	if got := OfFile(main).In; got != 16 {
		t.Errorf("in = %d after an append", got)
	}

	// A file that shrank was replaced: start over.
	os.WriteFile(main, []byte(usageLine("n1", 7, 0, 0, 0)), 0o644)
	if got := OfFile(main); got != (Tokens{In: 7}) {
		t.Errorf("after a replace = %+v", got)
	}
}

func TestContextIsTheSizeOfTheLatestAnswer(t *testing.T) {
	main := filepath.Join(t.TempDir(), "s1.jsonl")
	if ContextOf(main) != 0 || OfFile(main) != (Tokens{}) || ContextOf("") != 0 {
		t.Error("a missing transcript has tokens")
	}
	appendTo(t, main, usageLine("m1", 10, 5, 90_000, 9_990)+usageLine("m2", 20, 7, 149_000, 980))
	if got := ContextOf(main); got != 150_000 {
		t.Errorf("context = %d", got)
	}
	// A synthetic message is not an answer of the model.
	appendTo(t, main, `{"message": {"id": "m3", "model": "<synthetic>", "usage": {"input_tokens": 1}}}`+"\n")
	appendTo(t, main, "{broken\n"+`{"message": {"usage": "junk"}}`+"\n")
	if got := ContextOf(main); got != 150_000 {
		t.Errorf("context = %d after noise", got)
	}
	if got := OfFile(main).In; got != 31 {
		t.Errorf("in = %d", got)
	}
}

func TestModelIsTheOneOfTheLatestAnswer(t *testing.T) {
	main := filepath.Join(t.TempDir(), "s1.jsonl")
	if ModelOf(main) != "" || ModelOf("") != "" {
		t.Error("a missing transcript has a model")
	}
	appendTo(t, main, `{"message": {"id": "m1", "model": "claude-opus-4-5", "usage": {"input_tokens": 1}}}`+"\n")
	appendTo(t, main, `{"message": {"id": "m2", "model": "claude-sonnet-4-5", "usage": {"input_tokens": 1}}}`+"\n")
	// Neither a synthetic message nor one without a model changes it.
	appendTo(t, main, `{"message": {"id": "m3", "model": "<synthetic>", "usage": {"input_tokens": 1}}}`+"\n"+usageLine("m4", 1, 1, 0, 0))
	if got := ModelOf(main); got != "claude-sonnet-4-5" {
		t.Errorf("model = %q", got)
	}
}
