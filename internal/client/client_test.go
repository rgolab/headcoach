package client

import (
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
