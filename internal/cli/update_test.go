package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/humblebundledore/headcoach/internal/update"
	"github.com/humblebundledore/headcoach/internal/version"
)

func TestUpdate(t *testing.T) {
	f := newFixture(t)
	saved := version.Version
	t.Cleanup(func() { version.Version = saved })

	// A build from source is not replaced by a release.
	version.Version = "v0.1.1-15-g0123abc"
	if code, _, errOut := f.hc("update"); code != 1 || !strings.Contains(errOut, "built from source") {
		t.Errorf("source build gave %d %q", code, errOut)
	}

	latest := "v0.2.0"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name":"` + latest + `","body":"- the notes","html_url":"https://example.com/r"}`))
	}))
	t.Cleanup(srv.Close)
	api := update.APIURL
	update.APIURL = srv.URL
	t.Cleanup(func() { update.APIURL = api })

	version.Version = "v0.2.0"
	if out := f.ok("update"); !strings.Contains(out, "hc v0.2.0 is the latest release") {
		t.Errorf("up to date printed %q", out)
	}

	// A newer release shows its notes and waits for a yes.
	latest = "v0.3.0"
	asked := ""
	savedConfirm := confirmUpdate
	confirmUpdate = func(e *env, question string) bool { asked = question; return false }
	t.Cleanup(func() { confirmUpdate = savedConfirm })
	exe, _ := os.Executable()
	before, _ := os.Stat(exe)
	code, out, errOut := f.hc("update")
	if code != 1 || !strings.Contains(out, "hc v0.2.0 -> v0.3.0") || !strings.Contains(out, "- the notes") ||
		asked != "Install v0.3.0?" || !strings.Contains(errOut, "not updated") {
		t.Errorf("declined update gave %d %q %q, asked %q", code, out, errOut, asked)
	}
	if after, _ := os.Stat(exe); !after.ModTime().Equal(before.ModTime()) {
		t.Error("the binary changed without a yes")
	}
}
