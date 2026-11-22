package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/humblebundledore/headcoach/internal/config"
	"github.com/humblebundledore/headcoach/internal/review"
	"github.com/humblebundledore/headcoach/internal/store"
	"github.com/humblebundledore/headcoach/internal/tmux"
)

// fakeTmux records every call the server would make to tmux.
type fakeTmux struct {
	known        map[string]bool // panes of your own tmux
	privateKnown map[string]bool // panes of the board's own server
	available    bool
	installed    bool
	fail         string
	screen       string
	sent         []string // "pane|text|socket"
	focused      []string
	renamed      []string // "pane|name"
	killed       []string // "pane|socket"
	pressed      []string
	typed        []string // "pane|text"
	windows      []tmux.Window
	sessions     []tmux.Window
}

func newFakeTmux() *fakeTmux {
	return &fakeTmux{known: map[string]bool{}, privateKnown: map[string]bool{}, available: true, installed: true}
}

func (f *fakeTmux) panes(socket string) map[string]bool {
	if strings.HasSuffix(socket, "tmux.sock") {
		return f.privateKnown
	}
	return f.known
}

func (f *fakeTmux) err() error {
	if f.fail != "" {
		return errors.New(f.fail)
	}
	return nil
}

func (f *fakeTmux) Installed() bool                     { return f.installed }
func (f *fakeTmux) Available() bool                     { return f.available }
func (f *fakeTmux) Panes(socket string) map[string]bool { return f.panes(socket) }
func (f *fakeTmux) Capture(pane, socket string) string  { return f.screen }
func (f *fakeTmux) RenameWindow(pane, name, socket string) {
	f.renamed = append(f.renamed, pane+"|"+name)
}

func (f *fakeTmux) SendText(pane, text, socket string) error {
	if f.fail == "" {
		f.sent = append(f.sent, pane+"|"+text+"|"+socket)
	}
	return f.err()
}

func (f *fakeTmux) KillPane(pane, socket string) {
	f.killed = append(f.killed, pane+"|"+socket)
	delete(f.panes(socket), pane)
}

func (f *fakeTmux) SendKeys(pane string, keys []string, socket string) error {
	if err := tmux.CheckKeys(keys); err != nil {
		return err
	}
	f.pressed = append(f.pressed, pane+"|"+strings.Join(keys, " "))
	return nil
}

func (f *fakeTmux) TypeText(pane, text, socket string) error {
	if f.fail == "" {
		f.typed = append(f.typed, pane+"|"+text)
	}
	return f.err()
}

func (f *fakeTmux) Focus(pane, socket string) error {
	if f.fail == "" {
		f.focused = append(f.focused, pane)
	}
	return f.err()
}

func (f *fakeTmux) NewWindow(w tmux.Window) (string, error) {
	if f.fail != "" {
		return "", f.err()
	}
	pane := fmt.Sprintf("%%%d", 100+len(f.windows))
	f.windows = append(f.windows, w)
	f.known[pane] = true
	return pane, nil
}

func (f *fakeTmux) NewSession(w tmux.Window) (string, error) {
	if f.fail != "" {
		return "", f.err()
	}
	pane := fmt.Sprintf("%%%d", len(f.sessions))
	f.sessions = append(f.sessions, w)
	f.privateKnown[pane] = true
	return pane, nil
}

type obj = map[string]any

type fixture struct {
	t    *testing.T
	tmp  string
	cfg  *config.Config
	st   *store.Store
	tmux *fakeTmux
	srv  *Server
	h    http.Handler
	prs  map[string]review.PR // what the forge says about each pull request
}

// newFixture builds a daemon that opens windows in your own tmux, the way most of the suite expects.
func newFixture(t *testing.T) *fixture { return build(t, false) }

// newPrivate builds a daemon that starts sessions in a tmux server of its own.
func newPrivate(t *testing.T) *fixture { return build(t, true) }

func build(t *testing.T, private bool) *fixture {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.DB = filepath.Join(tmp, "state", "b.db")
	cfg.Profiles = []config.Profile{{Name: "team", Command: "claude-team"}, {Name: "personal", Command: "claude-personal"}}
	cfg.PrivateTmux = private
	f := &fixture{t: t, tmp: tmp, cfg: cfg, st: st, tmux: newFakeTmux()}
	f.srv = New(cfg, st, f.tmux)
	f.prs = map[string]review.PR{}
	f.srv.PRState = func(url string) review.PR { return f.prs[url] }
	f.srv.Ask = func(launcher, model, prompt, cwd string) (string, error) {
		t.Error("a test ran the real Claude Code")
		return "", nil
	}
	f.h = f.srv.Handler()
	return f
}

type response struct {
	status int
	header http.Header
	raw    []byte
	data   obj
}

// str reads a field of the answer as text.
func (r response) str(key string) string {
	s, _ := r.data[key].(string)
	return s
}

// do sends a request. body is nil, raw bytes, or a value sent as JSON.
func (f *fixture) do(method, path string, body any, headers ...string) response {
	f.t.Helper()
	var reader *bytes.Reader
	contentType := ""
	switch b := body.(type) {
	case nil:
		reader = bytes.NewReader(nil)
	case []byte:
		reader = bytes.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			f.t.Fatal(err)
		}
		reader, contentType = bytes.NewReader(raw), "application/json"
	}
	req := httptest.NewRequest(method, "http://127.0.0.1:7337"+path, reader)
	req.RemoteAddr = "127.0.0.1:50000" // the daemon listens on loopback: local unless a test says otherwise
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for i := 0; i < len(headers); i += 2 {
		switch headers[i] {
		case "Host":
			req.Host = headers[i+1]
		case "RemoteAddr":
			req.RemoteAddr = headers[i+1]
		default:
			req.Header.Set(headers[i], headers[i+1])
		}
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	out := response{status: rec.Code, header: rec.Header(), raw: rec.Body.Bytes()}
	_ = json.Unmarshal(out.raw, &out.data)
	return out
}

func (f *fixture) get(path string) response { return f.do("GET", path, nil) }

func (f *fixture) post(path string, body any) response { return f.do("POST", path, body) }

func (f *fixture) patch(path string, body any) response { return f.do("PATCH", path, body) }

// ok fails the test unless the answer is a 200.
func (f *fixture) ok(r response) response {
	f.t.Helper()
	if r.status != 200 {
		f.t.Fatalf("status %d: %s", r.status, r.raw)
	}
	return r
}

// wantError checks the status and that the body is {"error": "..."} and nothing else.
func (f *fixture) wantError(r response, status int) response {
	f.t.Helper()
	if r.status != status || len(r.data) != 1 || r.str("error") == "" {
		f.t.Errorf("got %d %s, want %d with an error", r.status, r.raw, status)
	}
	return r
}

// hook posts one hook event of a session, from pane %1 unless headers say otherwise.
func (f *fixture) hook(name, sid string, body obj, headers ...string) string {
	f.t.Helper()
	payload := obj{"hook_event_name": name, "session_id": sid}
	for k, v := range body {
		payload[k] = v
	}
	if len(headers) == 0 {
		headers = []string{"X-Tmux-Pane", "%1"}
	}
	return f.ok(f.do("POST", "/api/hook", payload, headers...)).str("task")
}

// newTask adds a task to the backlog.
func (f *fixture) newTask(extra obj) response {
	f.t.Helper()
	data := obj{"prompt": "do the thing", "cwd": f.tmp}
	for k, v := range extra {
		data[k] = v
	}
	return f.ok(f.post("/api/tasks", data))
}

// live makes a running task with a live session s1 in pane %1 and a transcript path.
func (f *fixture) live() string {
	f.t.Helper()
	f.tmux.known["%1"] = true
	if err := os.WriteFile(filepath.Join(f.tmp, "s1.jsonl"), nil, 0o644); err != nil {
		f.t.Fatal(err)
	}
	return f.hook("UserPromptSubmit", "s1",
		obj{"prompt": "hello", "transcript_path": filepath.Join(f.tmp, "s1.jsonl")},
		"X-Tmux-Pane", "%1", "X-Config-Dir", "/Users/x/.claude-personal")
}

func (f *fixture) task(id string) *store.Task {
	f.t.Helper()
	task, err := f.st.Task(id)
	if err != nil {
		f.t.Fatal(err)
	}
	return task
}

func (f *fixture) session(id string) *store.Session {
	f.t.Helper()
	sess, err := f.st.Session(id)
	if err != nil || sess == nil {
		f.t.Fatalf("session %s: %v, %v", id, sess, err)
	}
	return sess
}

func (f *fixture) count(table string) int {
	f.t.Helper()
	var n int
	if err := f.st.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func (f *fixture) history(id string) []string {
	f.t.Helper()
	rows, err := f.st.Query("SELECT state || ' ' || by FROM history WHERE task_id = ? ORDER BY id", id)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

func (f *fixture) write(name, text string) string {
	f.t.Helper()
	path := filepath.Join(f.tmp, name)
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		f.t.Fatal(err)
	}
	return path
}

func eq(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

// shellWords splits a command the way /bin/sh does, by asking /bin/sh.
func shellWords(t *testing.T, command string) []string {
	t.Helper()
	script := `for word in "$@"; do printf '%s\0' "$word"; done`
	out, err := execOutput("/bin/sh", "-c", "set -- "+command+"; "+script)
	if err != nil {
		t.Fatalf("the shell rejects %q: %v", command, err)
	}
	words := strings.Split(out, "\x00")
	return words[:len(words)-1]
}
