// Package update replaces the hc binary with the latest GitHub release, checked against its
// SHA256SUMS. It talks to GitHub only when asked to, and sends nothing about the user.
package update

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Where releases live. Tests point them at a fake server.
var (
	APIURL      = "https://api.github.com/repos/humblebundledore/headcoach/releases/latest"
	DownloadURL = "https://github.com/humblebundledore/headcoach/releases/download"
)

// Release is the latest published release.
type Release struct {
	Tag   string `json:"tag_name"`
	Notes string `json:"body"`
	URL   string `json:"html_url"`
}

var releaseRe = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

// Released tells whether version is a release, vX.Y.Z, rather than a build from source.
func Released(version string) bool { return releaseRe.MatchString(version) }

// Newer tells whether release a comes after release b.
func Newer(a, b string) bool {
	pa, pb := releaseRe.FindStringSubmatch(a), releaseRe.FindStringSubmatch(b)
	if pa == nil || pb == nil {
		return false
	}
	for i := 1; i <= 3; i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x > y
		}
	}
	return false
}

func client() *http.Client { return &http.Client{Timeout: 2 * time.Minute} }

func get(url, agent string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", agent)
	resp, err := client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// Latest asks GitHub for the latest release. agent is the User-Agent, hc and its version.
func Latest(agent string) (Release, error) {
	var rel Release
	body, err := get(APIURL, agent)
	if err != nil {
		return rel, err
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		return rel, fmt.Errorf("unexpected answer from GitHub: %w", err)
	}
	if !Released(rel.Tag) {
		return rel, fmt.Errorf("the latest release has no version tag: %q", rel.Tag)
	}
	return rel, nil
}

// Download fetches the binary of a release for an OS and an architecture, and checks it against
// the release's SHA256SUMS.
func Download(tag, goos, goarch, agent string) ([]byte, error) {
	name := "hc-" + goos + "-" + goarch
	sums, err := get(DownloadURL+"/"+tag+"/SHA256SUMS", agent)
	if err != nil {
		return nil, err
	}
	want := ""
	for _, line := range strings.Split(string(sums), "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			want = fields[0]
		}
	}
	if want == "" {
		return nil, fmt.Errorf("%s has no binary for %s/%s", tag, goos, goarch)
	}
	bin, err := get(DownloadURL+"/"+tag+"/"+name, agent)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(bin)
	if got := hex.EncodeToString(sum[:]); got != want {
		return nil, fmt.Errorf("%s does not match SHA256SUMS: got %s, want %s", name, got, want)
	}
	return bin, nil
}

// Replace swaps the binary at path for bin, in one rename, so a failure leaves the old one in place.
func Replace(path string, bin []byte) error {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".hc-update-*")
	if err != nil {
		return fmt.Errorf("cannot write next to %s: %w", path, err)
	}
	defer os.Remove(tmp.Name()) // a no-op once renamed
	if _, err := io.Copy(tmp, bytes.NewReader(bin)); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ErrSource is the answer of Check for a build from source: it updates the way it was built.
var ErrSource = errors.New("built from source: update it the same way, with go install ...@latest or git pull && make install && hc restart")

// Check asks GitHub for the latest release, and whether it is newer than current.
func Check(current string) (Release, bool, error) {
	if !Released(current) {
		return Release{}, false, ErrSource
	}
	rel, err := Latest("hc/" + current)
	if err != nil {
		return rel, false, err
	}
	return rel, Newer(rel.Tag, current), nil
}

// Install puts the binary of a release in place of the running one, for this OS and architecture.
// It returns where it was installed.
func Install(tag, current string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	bin, err := Download(tag, runtime.GOOS, runtime.GOARCH, "hc/"+current)
	if err != nil {
		return "", err
	}
	return exe, Replace(exe, bin)
}
