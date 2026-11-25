package herdr

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fakeHerdr puts a herdr on PATH that logs its socket and arguments, one call per line, and prints answer.
// It fails the way Herdr does when the socket is not "/run/h.sock".
func fakeHerdr(t *testing.T, answer string) string {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	script := `#!/bin/sh
echo "$HERDR_SOCKET_PATH|$HERDR_PANE_ID|$*" >> ` + calls + `
if [ "$HERDR_SOCKET_PATH" != /run/h.sock ]; then
  echo '{"error":{"code":"server_not_running","message":"no herdr server is running"}}' >&2
  exit 1
fi
cat <<'EOF'
` + answer + `
EOF
`
	if err := os.WriteFile(filepath.Join(dir, "herdr"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	t.Setenv("HERDR_PANE_ID", "w9:p9") // the daemon's own pane, when it runs in one, must not leak
	return calls
}

func calls(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

func TestAgentsListsTheClaudePanesOfOneServer(t *testing.T) {
	log := fakeHerdr(t, `{"result":{"agents":[
		{"agent":"claude","pane_id":"w1:p1","cwd":"/w/a","agent_session":{"value":"s1"}},
		{"agent":"claude","pane_id":"w1:p2","cwd":"/w/b"},
		{"agent":"codex","pane_id":"w1:p3","agent_session":{"value":"c1"}}]}}`)
	c := &Client{}
	want := map[string]Agent{"w1:p1": {"s1", "/w/a"}, "w1:p2": {"", "/w/b"}}
	if got := c.Agents("/run/h.sock"); !reflect.DeepEqual(got, want) {
		t.Errorf("Agents = %v, want %v", got, want)
	}
	c.Agents("/run/h.sock") // cached
	if got := c.Agents("/gone/h.sock"); len(got) != 0 {
		t.Errorf("a server that is gone has agents: %v", got)
	}
	want2 := []string{"/run/h.sock||agent list", "/gone/h.sock||agent list"}
	if got := calls(t, log); !reflect.DeepEqual(got, want2) {
		t.Errorf("calls = %q, want %q", got, want2)
	}
}

func TestPromptAndKeysGoToThePaneOverItsSocket(t *testing.T) {
	log := fakeHerdr(t, `{"result":{"type":"ok"}}`)
	c := &Client{}
	if err := c.Prompt("w1:p1", "--wait is text here", "/run/h.sock"); err != nil {
		t.Fatal(err)
	}
	if err := c.SendKeys("w1:p1", []string{"Down", "BTab", "Enter"}, "/run/h.sock"); err != nil {
		t.Fatal(err)
	}
	if err := c.SendKeys("w1:p1", []string{"C-c"}, "/run/h.sock"); err == nil {
		t.Error("a key outside the list was pressed")
	}
	want := []string{
		"/run/h.sock||agent prompt w1:p1 --wait is text here",
		"/run/h.sock||agent send-keys w1:p1 Down",
		"/run/h.sock||agent send-keys w1:p1 shift+tab",
		"/run/h.sock||agent send-keys w1:p1 Enter",
	}
	if got := calls(t, log); !reflect.DeepEqual(got, want) {
		t.Errorf("calls = %q, want %q", got, want)
	}
	if err := c.Prompt("w1:p1", "hi", "/gone/h.sock"); err == nil || err.Error() != "no herdr server is running" {
		t.Errorf("Prompt over a gone socket = %v", err)
	}
}

func TestWithoutHerdrNothingIsLive(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	c := &Client{}
	if got := c.Agents("/run/h.sock"); len(got) != 0 {
		t.Errorf("Agents = %v", got)
	}
	if err := c.Prompt("w1:p1", "hi", "/run/h.sock"); err == nil || err.Error() != "herdr is not installed" {
		t.Errorf("Prompt = %v", err)
	}
}

func TestAttachPassesTheTerminalThroughUntilCtrlBracket(t *testing.T) {
	dir := t.TempDir()
	log, seen := filepath.Join(dir, "calls"), filepath.Join(dir, "seen")
	// The first run of a new script can take a while: Ctrl-] must not hang it up before its trap is set.
	script := `#!/bin/sh
trap 'echo hung up; exit 0' HUP
echo "$HERDR_SOCKET_PATH|$HERDR_PANE_ID|$*" > ` + log + `
while read -r line; do echo "got:$line"; echo "$line" >> ` + seen + `; done
`
	if err := os.WriteFile(filepath.Join(dir, "herdr"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	t.Setenv("HERDR_PANE_ID", "w9:p9")
	keys, typing, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer typing.Close()
	var screen strings.Builder
	a := NewAttach(AttachCommand("w1:p2", "/run/h.sock"))
	a.SetStdin(keys)
	a.SetStdout(&screen)
	done := make(chan error, 1)
	go func() { done <- a.Run() }()
	typing.WriteString("hi\n")
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if raw, _ := os.ReadFile(seen); strings.Contains(string(raw), "hi") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("herdr never read the line")
		}
	}
	typing.WriteString("bye\x1d")
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Ctrl-] did not leave")
	}
	if got := screen.String(); !strings.Contains(got, "got:hi") || !strings.Contains(got, "hung up") {
		t.Errorf("screen = %q", got)
	}
	if got := calls(t, log); !reflect.DeepEqual(got, []string{"/run/h.sock||agent attach w1:p2"}) {
		t.Errorf("calls = %q", got)
	}
}

func TestBeforeDetach(t *testing.T) {
	for _, c := range []struct {
		in, want string
		detach   bool
	}{
		{"abc", "abc", false},
		{"ab\x1dc", "ab", true},
		{"\x1d", "", true},
		{"ab\x1b[93;5ucd", "ab", true},
		{"\x1b[93;5:1u", "", true},
		{"\x1b[93;69u", "", true},
		{"x\x1b[27;5;93~", "x", true},
		{"\x1b[93;5:3u", "\x1b[93;5:3u", false},
		{"\x1b[93;6u", "\x1b[93;6u", false},
	} {
		got, detach := BeforeDetach([]byte(c.in))
		if string(got) != c.want || detach != c.detach {
			t.Errorf("BeforeDetach(%q) = %q, %v", c.in, got, detach)
		}
	}
}
