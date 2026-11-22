package update

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersions(t *testing.T) {
	for v, want := range map[string]bool{"v1.2.0": true, "v10.0.1": true, "dev": false, "v0.1.1-15-g0123abc": false, "0.2.0": false} {
		if Released(v) != want {
			t.Errorf("Released(%q) = %v", v, !want)
		}
	}
	for _, c := range []struct {
		a, b string
		want bool
	}{{"v1.2.0", "v0.1.1", true}, {"v0.10.0", "v0.9.9", true}, {"v1.2.0", "v1.2.0", false}, {"v0.1.9", "v1.2.0", false}, {"v1.0.0", "dev", false}} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%s, %s) = %v", c.a, c.b, got)
		}
	}
}

// fake serves a release whose binary is bin, and whose SHA256SUMS lists sum for it.
func fake(t *testing.T, bin, sum string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "hc/") {
			http.Error(w, "no agent", 400)
			return
		}
		switch r.URL.Path {
		case "/latest":
			w.Write([]byte(`{"tag_name":"v1.3.0","body":"- new things","html_url":"https://example.com/v1.3.0"}`))
		case "/download/v1.3.0/SHA256SUMS":
			w.Write([]byte("abc  hc-plan9-386\n" + sum + "  hc-linux-arm64\n"))
		case "/download/v1.3.0/hc-linux-arm64":
			w.Write([]byte(bin))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	api, dl := APIURL, DownloadURL
	APIURL, DownloadURL = srv.URL+"/latest", srv.URL+"/download"
	t.Cleanup(func() { APIURL, DownloadURL = api, dl })
}

func TestLatestAndDownload(t *testing.T) {
	sum := sha256.Sum256([]byte("new binary"))
	fake(t, "new binary", hex.EncodeToString(sum[:]))
	rel, err := Latest("hc/v1.2.0")
	if err != nil || rel.Tag != "v1.3.0" || rel.Notes != "- new things" {
		t.Fatalf("Latest = %+v, %v", rel, err)
	}
	bin, err := Download(rel.Tag, "linux", "arm64", "hc/v1.2.0")
	if err != nil || string(bin) != "new binary" {
		t.Fatalf("Download = %q, %v", bin, err)
	}
	if _, err := Download(rel.Tag, "darwin", "arm64", "hc/v1.2.0"); err == nil || !strings.Contains(err.Error(), "no binary for darwin/arm64") {
		t.Errorf("a missing platform: %v", err)
	}
}

func TestDownloadRefusesABadSum(t *testing.T) {
	fake(t, "tampered", strings.Repeat("0", 64))
	if _, err := Download("v1.3.0", "linux", "arm64", "hc/v1.2.0"); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Errorf("a bad sum was accepted: %v", err)
	}
}

func TestReplace(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "hc-real")
	link := filepath.Join(dir, "hc")
	os.WriteFile(real, []byte("old"), 0o755)
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := Replace(link, []byte("new")); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(real)
	info, _ := os.Stat(real)
	if string(got) != "new" || info.Mode().Perm() != 0o755 {
		t.Errorf("binary = %q, mode %v", got, info.Mode())
	}
	if target, _ := os.Readlink(link); target != real {
		t.Errorf("the link was replaced: %s", target)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("a temp file was left: %v", entries)
	}
}
