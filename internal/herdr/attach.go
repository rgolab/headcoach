package herdr

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
	"github.com/muesli/cancelreader"
)

// DetachKey is Ctrl-], the board's one key to leave a session. Herdr's own detach is a prefix key that a
// Herdr around hc would take for itself, so Ctrl-] hangs the attach client up instead: the agent runs on.
const DetachKey = 0x1d

// env is the environment of a herdr command: none of the HERDR_ variables of a pane hc may run in, nor TMUX.
func env() []string {
	var out []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HERDR_") && !strings.HasPrefix(kv, "TMUX=") {
			out = append(out, kv)
		}
	}
	return out
}

// AttachCommand is the command that puts a terminal inside the agent of a pane. It runs with AttachEnv.
func AttachCommand(pane, socket string) []string {
	return []string{"env", "HERDR_SOCKET_PATH=" + socket, "herdr", "agent", "attach", pane}
}

// AttachEnv is the environment to attach with.
func AttachEnv() []string { return env() }

// detachSeq is Ctrl-] once the attached Herdr has turned on the kitty keyboard protocol, which it does, or
// xterm's modifyOtherKeys: `CSI 93;5u`, with caps or num lock and a press event type, or `CSI 27;5;93~`.
var detachSeq = regexp.MustCompile(`\x1b\[93;(5|69|133|197)(:1)?u|\x1b\[27;5;93~`)

// BeforeDetach returns the keys typed before Ctrl-], and whether Ctrl-] was typed.
func BeforeDetach(keys []byte) ([]byte, bool) {
	i := bytes.IndexByte(keys, DetachKey)
	if loc := detachSeq.FindIndex(keys); loc != nil && (i < 0 || loc[0] < i) {
		i = loc[0]
	}
	if i >= 0 {
		return keys[:i], true
	}
	return keys, false
}

// Attach runs an attach command in a terminal of its own, and passes this terminal through to it until the
// command leaves or Ctrl-] hangs it up. It is a tea.ExecCommand, for the board.
type Attach struct {
	argv           []string
	stdin          io.Reader
	stdout, stderr io.Writer
}

// NewAttach builds an Attach on the process's own terminal.
func NewAttach(argv []string) *Attach {
	return &Attach{argv: argv, stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr}
}

func (a *Attach) SetStdin(r io.Reader)  { a.stdin = r }
func (a *Attach) SetStdout(w io.Writer) { a.stdout = w }
func (a *Attach) SetStderr(w io.Writer) { a.stderr = w }

// Run attaches, and returns once the attach command has left.
func (a *Attach) Run() error {
	cmd := exec.Command(a.argv[0], a.argv[1:]...)
	cmd.Env = AttachEnv()
	size := &pty.Winsize{Cols: 80, Rows: 24}
	tty, _ := a.stdin.(*os.File)
	if tty != nil && term.IsTerminal(tty.Fd()) {
		if cols, rows, err := term.GetSize(tty.Fd()); err == nil {
			size = &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}
		}
		state, err := term.MakeRaw(tty.Fd())
		if err != nil {
			return err
		}
		defer term.Restore(tty.Fd(), state)
	} else {
		tty = nil
	}
	inner, err := pty.StartWithSize(cmd, size)
	if err != nil {
		return err
	}
	defer inner.Close()

	resized := make(chan os.Signal, 1)
	signal.Notify(resized, syscall.SIGWINCH)
	defer func() { signal.Stop(resized); close(resized) }()
	go func() {
		for range resized {
			if tty != nil {
				_ = pty.InheritSize(tty, inner)
			}
		}
	}()

	// A read left waiting after the attach would eat the board's next key: it is canceled on the way out.
	keys, err := cancelreader.NewReader(a.stdin)
	if err != nil {
		return err
	}
	defer keys.Close()
	defer keys.Cancel()
	var hungUp atomic.Bool
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := keys.Read(buf)
			typed, detach := BeforeDetach(buf[:n])
			if len(typed) > 0 {
				if _, werr := inner.Write(typed); werr != nil {
					return
				}
			}
			if detach {
				hungUp.Store(true)
				_ = cmd.Process.Signal(syscall.SIGHUP)
				return
			}
			if err != nil {
				return
			}
		}
	}()
	_, _ = io.Copy(a.stdout, inner) // ends once the attach command has left
	err = cmd.Wait()
	if hungUp.Load() {
		// Pop the kitty keyboard mode Herdr pushed, in case the hang-up left it on; a pop on an empty stack does nothing.
		_, _ = io.WriteString(a.stdout, "\x1b[<u")
	}
	return err
}
