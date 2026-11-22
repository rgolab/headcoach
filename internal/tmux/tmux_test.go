package tmux

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestAttachCommandShowsTheWayOutAndAttachQuietDoesNot(t *testing.T) {
	quiet := []string{"tmux", "-S", "/tmp/s", "attach-session", "-t", "%1"}
	if got := AttachQuiet("%1", "/tmp/s"); !reflect.DeepEqual(got, quiet) {
		t.Errorf("AttachQuiet = %v, want %v", got, quiet)
	}
	want := append(quiet, ";", "display-message", "-d", "3000", DetachHint)
	if got := AttachCommand("%1", "/tmp/s"); !reflect.DeepEqual(got, want) {
		t.Errorf("AttachCommand = %v, want %v", got, want)
	}
}

func TestQuietDetach(t *testing.T) {
	var out bytes.Buffer
	w := QuietDetach(&out)
	for _, chunk := range []string{"\x1b[?1049l[deta", "ched (from session 0123abcd-demo)]\n", "[lost tty]\n", "[detached]\n", "[exited]\n"} {
		if n, err := w.Write([]byte(chunk)); err != nil || n != len(chunk) {
			t.Fatalf("Write(%q) = %d, %v", chunk, n, err)
		}
	}
	if got, want := out.String(), "\x1b[?1049l[lost tty]\n[exited]\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTypeTextTypesWhatTmuxWouldReadAsFlagsOrSeparators(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	dir, err := os.MkdirTemp("/tmp", "hc") // a socket path has a short limit
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket, out := filepath.Join(dir, "s"), filepath.Join(dir, "out")
	if _, err := run(socket, "", "new-session", "-d", "-s", "t", "cat > "+out); err != nil {
		t.Fatal(err)
	}
	defer run(socket, "", "kill-server")
	c := &Client{}
	for _, text := range []string{"-1 is fine", " -H", "both;", ";", "a;b", `C:\tmp\;;`} {
		if err := c.TypeText("t", text, socket); err != nil {
			t.Fatalf("TypeText(%q): %v", text, err)
		}
	}
	if _, err := run(socket, "", "send-keys", "-t", "t", "Enter", "C-d"); err != nil {
		t.Fatal(err)
	}
	want := "-1 is fine -Hboth;;a;bC:\\tmp\\;;\n"
	for i := 0; i < 50; i++ {
		if got, _ := os.ReadFile(out); string(got) == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	got, _ := os.ReadFile(out)
	t.Errorf("typed %q, want %q", got, want)
}
