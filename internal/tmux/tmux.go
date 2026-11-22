// Package tmux is everything that touches tmux. The board talks to live sessions through their pane.
package tmux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	detachKey = "C-]"
	// DetachHint is shown when a terminal enters a session of the board's own tmux server.
	DetachHint = "Ctrl-] returns to where you came from"
	// MainSessionHint replaces DetachHint when a sub-agent's row was entered: it has no terminal of its own.
	MainSessionHint = "Sub-agents run inside this main session. " + DetachHint
	// PrivateConf configures the board's own tmux server: no status bar, no prefix key, one key to leave.
	// It should feel like plain Claude.
	PrivateConf = `set -g status off
set -g prefix None
unbind-key -a
bind-key -n ` + detachKey + ` detach-client
set -g mouse on
set -g history-limit 50000
set -g default-size 200x50
set -g focus-events on
set -s escape-time 0
set -s extended-keys on
`
)

// keys are the only keys SendKeys presses: enough to answer a dialog, not enough to type a command.
var keys = map[string]bool{
	"Enter": true, "Escape": true, "Up": true, "Down": true, "Left": true, "Right": true, "Tab": true,
	"BTab": true, "Space": true, "y": true, "n": true,
	"0": true, "1": true, "2": true, "3": true, "4": true, "5": true, "6": true, "7": true, "8": true, "9": true,
}

// CheckKeys tells whether every key may be pressed by SendKeys.
func CheckKeys(pressed []string) error {
	ok := len(pressed) > 0
	for _, key := range pressed {
		ok = ok && keys[key]
	}
	if ok {
		return nil
	}
	names := make([]string, 0, len(keys))
	for key := range keys {
		names = append(names, key)
	}
	sort.Strings(names)
	return fmt.Errorf("Keys must be chosen from: %s", strings.Join(names, ", "))
}

// Window describes a session to open. Socket and Conf only matter to NewSession, Session to NewWindow.
type Window struct {
	Cwd     string
	Command string
	Name    string
	Env     []string // KEY=value
	Session string   // the tmux session to open the window in; empty means the most recent one
	Socket  string   // the socket of the board's own server
	Conf    string   // the config file of the board's own server
}

type paneCache struct {
	at    time.Time
	known map[string]bool
}

// Client runs tmux commands. The zero value is ready to use.
type Client struct {
	mu    sync.Mutex
	cache map[string]paneCache // by socket
}

// run runs one tmux command. socket picks the server; empty means the default one.
func run(socket, stdin string, args ...string) (string, error) {
	if socket != "" {
		args = append([]string{"-S", socket}, args...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	name := args[0]
	if socket != "" {
		name = args[2]
	}
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return "", errors.New("tmux is not installed")
	case ctx.Err() != nil:
		return "", fmt.Errorf("tmux %s timed out", name)
	case err != nil:
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return "", errors.New(detail)
		}
		return "", fmt.Errorf("tmux %s failed", name)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Installed tells whether the tmux program exists.
func (c *Client) Installed() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// Available tells whether your own tmux server is running.
func (c *Client) Available() bool {
	if !c.Installed() {
		return false
	}
	_, err := run("", "", "list-sessions")
	return err == nil
}

// Panes returns the ids of every pane on one tmux server. Cached for two seconds: the board asks on
// each refresh.
func (c *Client) Panes(socket string) map[string]bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cached, ok := c.cache[socket]; ok && time.Since(cached.at) < 2*time.Second {
		return cached.known
	}
	known := map[string]bool{}
	if out, err := run(socket, "", "list-panes", "-a", "-F", "#{pane_id}"); err == nil {
		for _, pane := range strings.Fields(out) {
			known[pane] = true
		}
	}
	if c.cache == nil {
		c.cache = map[string]paneCache{}
	}
	c.cache[socket] = paneCache{time.Now(), known}
	return known
}

func (c *Client) forget() {
	c.mu.Lock()
	c.cache = nil
	c.mu.Unlock()
}

// SendText types a message into a pane and submits it. Bracketed paste keeps multi-line text in one message.
func (c *Client) SendText(pane, text, socket string) error {
	buffer := fmt.Sprintf("hc-%d", os.Getpid())
	if _, err := run(socket, text, "load-buffer", "-b", buffer, "-"); err != nil {
		return err
	}
	if _, err := run(socket, "", "paste-buffer", "-d", "-p", "-b", buffer, "-t", pane); err != nil {
		return err
	}
	time.Sleep(250 * time.Millisecond)
	_, err := run(socket, "", "send-keys", "-t", pane, "Enter")
	return err
}

// RenameWindow names the window that holds a pane. A missing pane is not worth failing a hook for.
func (c *Client) RenameWindow(pane, name, socket string) {
	_, _ = run(socket, "", "rename-window", "-t", pane, name)
}

// Capture returns the text a pane is showing right now. Empty when the pane cannot be read.
func (c *Client) Capture(pane, socket string) string {
	out, _ := run(socket, "", "capture-pane", "-p", "-t", pane)
	return out
}

// KillPane closes a pane, and its window when it was the only pane. A pane already gone is fine.
func (c *Client) KillPane(pane, socket string) {
	_, _ = run(socket, "", "kill-pane", "-t", pane)
	c.forget()
}

// SendKeys presses navigation keys in a pane, for answering a dialog.
func (c *Client) SendKeys(pane string, pressed []string, socket string) error {
	if err := CheckKeys(pressed); err != nil {
		return err
	}
	for _, key := range pressed {
		if _, err := run(socket, "", "send-keys", "-t", pane, key); err != nil {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

// TypeText types text in a pane as if on the keyboard, without submitting it: tmux reads no key names in it.
// A trailing ; would end the tmux command, and escaping it eats backslashes: it is sent as its byte instead.
func (c *Client) TypeText(pane, text, socket string) error {
	words := strings.TrimRight(text, ";")
	if words != "" {
		if _, err := run(socket, "", "send-keys", "-l", "-t", pane, "--", words); err != nil {
			return err
		}
	}
	for range len(text) - len(words) {
		if _, err := run(socket, "", "send-keys", "-H", "-t", pane, "3b"); err != nil {
			return err
		}
	}
	return nil
}

// Focus brings a pane to the front of whichever tmux client is attached.
func (c *Client) Focus(pane, socket string) error {
	session, err := run(socket, "", "display-message", "-p", "-t", pane, "#{session_name}")
	if err != nil {
		return err
	}
	if _, err := run(socket, "", "select-window", "-t", pane); err != nil {
		return err
	}
	if _, err := run(socket, "", "select-pane", "-t", pane); err != nil {
		return err
	}
	_, _ = run(socket, "", "switch-client", "-t", session) // fails when no client is attached, which is fine
	return nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func (w Window) args() []string {
	var args []string
	if w.Cwd != "" && isDir(w.Cwd) {
		args = append(args, "-c", w.Cwd)
	}
	if w.Session != "" {
		args = append(args, "-t", w.Session+":")
	}
	for _, kv := range w.Env {
		args = append(args, "-e", kv)
	}
	return append(args, w.Command)
}

// NewWindow opens a detached window in your own tmux. It returns the new pane id.
func (c *Client) NewWindow(w Window) (string, error) {
	args := append([]string{"new-window", "-d", "-P", "-F", "#{pane_id}", "-n", w.Name}, w.args()...)
	pane, err := run("", "", args...)
	c.forget()
	return pane, err
}

// NewSession opens a detached session of its own on the board's private server. It returns the new pane id.
//
// One tmux session per task: two terminals can then look at two tasks without following each other.
func (c *Client) NewSession(w Window) (string, error) {
	if err := os.MkdirAll(filepath.Dir(w.Conf), 0o755); err != nil {
		return "", err
	}
	if current, err := os.ReadFile(w.Conf); err != nil || string(current) != PrivateConf {
		if err := os.WriteFile(w.Conf, []byte(PrivateConf), 0o644); err != nil {
			return "", err
		}
	}
	taken := map[string]bool{}
	if out, err := run(w.Socket, "", "list-sessions", "-F", "#{session_name}"); err == nil {
		for _, name := range strings.Fields(out) {
			taken[name] = true
		}
	}
	unique := w.Name
	for n := 2; taken[unique]; n++ {
		unique = fmt.Sprintf("%s-%d", w.Name, n)
	}
	w.Session = "" // a session of its own, not a window in another
	args := append([]string{"-f", w.Conf, "new-session", "-d", "-P", "-F", "#{pane_id}", "-s", unique, "-n", w.Name},
		w.args()...)
	pane, err := run(w.Socket, "", args...)
	c.forget()
	return pane, err
}

// AttachCommand is the command that puts a terminal inside a session of the private server, with the
// way out shown.
func AttachCommand(pane, socket string) []string {
	return append(AttachQuiet(pane, socket), ";", "display-message", "-d", "3000", DetachHint)
}

// AttachQuiet is AttachCommand without the way out shown, for a client that has its own.
func AttachQuiet(pane, socket string) []string {
	return []string{"tmux", "-S", socket, "attach-session", "-t", pane}
}

// AttachEnv is the environment to attach with: without TMUX set, tmux agrees to attach from inside
// another tmux.
func AttachEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "TMUX=") {
			env = append(env, kv)
		}
	}
	return env
}

// QuietDetach passes what tmux prints on stdout to w, without the "[detached ...]" line it prints
// when a client leaves. Other exit reasons, such as "[lost tty]", still show.
func QuietDetach(w io.Writer) io.Writer { return &quietDetach{w: w} }

type quietDetach struct {
	w    io.Writer
	line []byte
}

func (q *quietDetach) Write(p []byte) (int, error) {
	q.line = append(q.line, p...)
	for {
		i := bytes.IndexByte(q.line, '\n')
		if i < 0 {
			break
		}
		line := q.line[:i+1]
		// The message follows the codes that restore the terminal, on the same line: keep those.
		if at := bytes.Index(line, []byte("[detached")); at >= 0 {
			line = line[:at]
		}
		if _, err := q.w.Write(line); err != nil {
			return len(p), err
		}
		q.line = q.line[i+1:]
	}
	return len(p), nil
}
