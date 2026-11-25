package cli

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/rouxlabs/headcoach/internal/config"
	"github.com/rouxlabs/headcoach/internal/install"
	"github.com/rouxlabs/headcoach/internal/server"
	"github.com/rouxlabs/headcoach/internal/store"
	"github.com/rouxlabs/headcoach/internal/tmux"
	"github.com/rouxlabs/headcoach/internal/version"
)

// cmdServe runs the daemon until it is told to stop.
func cmdServe(e *env, p parsed) error {
	cfg, err := config.Load("")
	if err != nil {
		return err
	}
	_, _ = install.HookScript() // an upgrade may ship a newer hook; a failure here must not stop the daemon
	st, err := store.Open(cfg.DB)
	if err != nil {
		return err
	}
	defer st.Close()

	daemon := server.New(cfg, st, &tmux.Client{})
	handler := daemon.Handler()
	loopsDone := make(chan struct{})
	defer close(loopsDone)
	go daemon.RunSchedules(loopsDone)
	go daemon.RunInbox(loopsDone)
	if p.has("verbose") {
		handler = logged(handler)
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("Cannot listen on %s: %v", addr, err)
	}
	// No write timeout: asking about a session takes as long as Claude Code takes to answer.
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("hc %s serves %s, database %s", version.Version, cfg.URL(), cfg.DB)
	if p.has("tailscale") {
		go publish(cfg.Port, loopsDone)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	failed := make(chan error, 1)
	go func() { failed <- srv.Serve(listener) }()
	select {
	case err := <-failed:
		return err
	case <-stop:
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
}

// publish puts the board on the tailnet, trying again for a while when tailscale is not up yet.
// It never stops the daemon: the board keeps serving 127.0.0.1 whatever tailscale says.
func publish(port int, done <-chan struct{}) {
	for try := 1; ; try++ {
		url, err := install.TailscalePublish(port)
		switch {
		case err == nil:
			log.Printf("tailscale serves the board at %s", url)
			return
		case !errors.Is(err, install.ErrTailscaleDown) && errors.Is(err, install.ErrTailscaleSkipped):
			log.Printf("tailscale: %v", err)
			return
		case try == 20:
			log.Printf("tailscale: gave up: %v", err)
			return
		case try == 1:
			log.Printf("tailscale: %v, trying again for 10 minutes", err)
		}
		select {
		case <-done:
			return
		case <-time.After(30 * time.Second):
		}
	}
}

// logged prints one line per request.
func logged(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		seen := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(seen, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, seen.status, time.Since(start).Round(time.Millisecond))
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// Unwrap lets an attach take the connection over: a WebSocket hijacks it from the writer underneath.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
