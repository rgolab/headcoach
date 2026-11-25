package install

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeTailscale stands in for the tailscale CLI. A missing state means the CLI is not installed;
// served is what 443 proxies "/" to. It returns the commands that change Serve.
func fakeTailscale(t *testing.T, state, served string) *[]string {
	t.Helper()
	var calls []string
	oldRun, oldLook := run, lookPath
	t.Cleanup(func() { run, lookPath = oldRun, oldLook })
	lookPath = func(name string) (string, error) {
		if name == "tailscale" && state != "" {
			return "/usr/local/bin/tailscale", nil
		}
		return "", errors.New("not found")
	}
	run = func(args ...string) (int, string, error) {
		command := strings.Join(args[1:], " ")
		switch command {
		case "status --json":
			if state == "Stopped" {
				return 1, "failed to connect to local tailscaled", nil
			}
			return 0, `{"BackendState":"` + state + `","Self":{"DNSName":"mac.tail1.ts.net."}}`, nil
		case "serve status --json":
			if served == "" {
				return 0, "\n", nil
			}
			return 0, `{"Web":{"mac.tail1.ts.net:443":{"Handlers":{"/":{"Proxy":"` + served + `"}}}}}`, nil
		}
		calls = append(calls, command)
		return 0, "", nil
	}
	return &calls
}

func TestTailscalePublish(t *testing.T) {
	for _, c := range []struct {
		name, state, served string
		down, skip          bool
		calls               []string
	}{
		{name: "fresh", state: "Running", calls: []string{"serve --bg --https=443 http://127.0.0.1:7337"}},
		{name: "already ours", state: "Running", served: "http://127.0.0.1:7337"},
		{name: "443 taken", state: "Running", served: "http://127.0.0.1:3000", skip: true},
		{name: "no CLI", skip: true},
		{name: "logged out", state: "NeedsLogin", skip: true, down: true},
		{name: "daemon not running", state: "Stopped", skip: true, down: true},
	} {
		calls := fakeTailscale(t, c.state, c.served)
		url, err := TailscalePublish(7337)
		if errors.Is(err, ErrTailscaleSkipped) != c.skip || errors.Is(err, ErrTailscaleDown) != c.down {
			t.Errorf("%s: err = %v", c.name, err)
		}
		if !c.skip && (err != nil || url != "https://mac.tail1.ts.net") {
			t.Errorf("%s: url = %q, %v", c.name, url, err)
		}
		if len(*calls) > 0 || len(c.calls) > 0 {
			if !reflect.DeepEqual(*calls, c.calls) {
				t.Errorf("%s ran %v, want %v", c.name, *calls, c.calls)
			}
		}
	}
}

func TestTailscaleUnpublish(t *testing.T) {
	for _, c := range []struct {
		name, state, served string
		removed             bool
	}{
		{name: "ours", state: "Running", served: "http://127.0.0.1:7337", removed: true},
		{name: "someone else's", state: "Running", served: "http://127.0.0.1:3000"},
		{name: "nothing served", state: "Running"},
		{name: "no CLI"},
		{name: "logged out", state: "NeedsLogin"},
	} {
		calls := fakeTailscale(t, c.state, c.served)
		removed, err := TailscaleUnpublish(7337)
		if err != nil || removed != c.removed {
			t.Errorf("%s: removed = %v, %v", c.name, removed, err)
		}
		var want []string
		if c.removed {
			want = []string{"serve --https=443 --set-path=/ off"}
		}
		if len(*calls) > 0 || want != nil {
			if !reflect.DeepEqual(*calls, want) {
				t.Errorf("%s ran %v, want %v", c.name, *calls, want)
			}
		}
	}
}

func TestServiceTailscale(t *testing.T) {
	isolate(t)
	fakeManager(t, "darwin")
	for _, tailscale := range []bool{true, false} {
		if _, err := ServiceInstall(tailscale); err != nil {
			t.Fatal(err)
		}
		launcher, _ := os.ReadFile(filepath.Join(os.Getenv("HC_DATA_DIR"), "headcoach"))
		if ServiceTailscale() != tailscale || strings.Contains(string(launcher), "--tailscale") != tailscale {
			t.Errorf("tailscale %v: launcher %q", tailscale, launcher)
		}
	}
	if _, err := ServiceUninstall(); err != nil || ServiceTailscale() {
		t.Errorf("uninstall = %v, tailscale = %v", err, ServiceTailscale())
	}
}
