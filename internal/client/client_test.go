package client

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A daemon too slow to answer is told apart from one that is not there: the slow one may have done
// what was asked.
func TestNoAnswerIsNotNoDaemon(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
	}))
	defer slow.Close()
	err := New(slow.URL).CallFor(50*time.Millisecond, "POST", "/api/tasks", map[string]string{"title": "x"}, nil)
	var late NoAnswer
	if !errors.As(err, &late) || !strings.Contains(err.Error(), "did not answer within 50ms") {
		t.Fatalf("a slow daemon: %v", err)
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gone := "http://" + l.Addr().String()
	l.Close()
	err = New(gone).CallFor(time.Second, "GET", "/api/tasks", nil, nil)
	if errors.As(err, &late) || !strings.HasPrefix(err.Error(), "No daemon at "+gone) {
		t.Fatalf("no daemon: %v", err)
	}
}

// A command an agent runs tells the daemon which session it runs in; one you type outside a session
// sends nothing, and counts as yours.
func TestCallerHeaders(t *testing.T) {
	var got http.Header
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.Header }))
	defer daemon.Close()
	t.Setenv("CLAUDECODE", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("HC_TASK", "")
	if err := New(daemon.URL).Call("GET", "/api/tasks", nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"X-HC-Session", "X-HC-Task", "X-HC-Agent"} {
		if got.Get(name) != "" {
			t.Errorf("outside a session, %s = %q", name, got.Get(name))
		}
	}
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "s1")
	t.Setenv("HC_TASK", "t1")
	if err := New(daemon.URL).Call("POST", "/api/tasks/t2/reply", map[string]string{"text": "x"}, nil); err != nil {
		t.Fatal(err)
	}
	if got.Get("X-HC-Session") != "s1" || got.Get("X-HC-Task") != "t1" || got.Get("X-HC-Agent") != "1" {
		t.Errorf("inside a session: %v", got)
	}
}

// A daemon from before pins answers with the task as it was: the pin did not happen, and says so.
func TestPinAgainstAnOlderDaemon(t *testing.T) {
	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"id": "task0001", "short": "task0001"})
	}))
	defer old.Close()
	if _, err := New(old.URL).Pin("task0001", true); err == nil || !strings.Contains(err.Error(), "hc restart") {
		t.Errorf("pin against an older daemon: %v", err)
	}
	if _, err := New(old.URL).Pin("task0001", false); err != nil {
		t.Errorf("unpin against an older daemon: %v", err)
	}
}
