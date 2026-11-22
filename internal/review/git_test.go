package review

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// run runs git in dir, with no global or system config so the machine's settings do not leak in.
func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=T", "-c", "user.email=t@example.com",
		"-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func commit(t *testing.T, dir, file, text string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "add", file)
	run(t, dir, "commit", "-q", "-m", file)
	return run(t, dir, "rev-parse", "HEAD")
}

// clone makes a remote with one commit on main and a clone of it.
func clone(t *testing.T) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	tmp := t.TempDir()
	remote, work := filepath.Join(tmp, "remote.git"), filepath.Join(tmp, "work")
	run(t, tmp, "init", "-q", "--bare", "-b", "main", remote)
	run(t, tmp, "clone", "-q", remote, work)
	run(t, work, "checkout", "-q", "-b", "main")
	commit(t, work, "a", "one\n")
	run(t, work, "push", "-q", "-u", "origin", "main")
	run(t, work, "remote", "set-head", "origin", "main")
	return remote, work
}

func TestInspectOnTheDefaultBranch(t *testing.T) {
	_, work := clone(t)
	base := run(t, work, "rev-parse", "HEAD")
	commit(t, work, "b", "two\nthree\n")
	head := commit(t, work, "a", "one more\n")
	got := Inspect(filepath.Join(work), base)
	want := Change{Branch: "main", Default: true, From: base, To: head, Commits: 2, Files: 2, Added: 3, Deleted: 1, Unpushed: 2}
	if *got != want {
		t.Errorf("change = %+v, want %+v", *got, want)
	}
	if got.Range() != base[:7]+".."+head[:7] {
		t.Errorf("range = %q", got.Range())
	}
	run(t, work, "push", "-q")
	// A file git does not track is not part of the change.
	os.WriteFile(filepath.Join(work, "c"), []byte("new"), 0o644)
	got = Inspect(work, base)
	if got.Unpushed != 0 || got.Dirty || got.Commits != 2 {
		t.Errorf("after the push = %+v", *got)
	}
	os.WriteFile(filepath.Join(work, "a"), []byte("changed"), 0o644)
	if got = Inspect(work, base); !got.Dirty {
		t.Errorf("a changed file = %+v", *got)
	}
}

func TestInspectOnABranch(t *testing.T) {
	_, work := clone(t)
	run(t, work, "checkout", "-q", "-b", "feature")
	fork := run(t, work, "rev-parse", "HEAD")
	commit(t, work, "b", "x\n")
	// Without a base, the change starts where the branch left the default one.
	got := Inspect(work, "")
	if got.Default || got.Branch != "feature" || got.From != fork || got.Commits != 1 || got.Unpushed != -1 {
		t.Errorf("change = %+v", *got)
	}
	// A base that is not in the history, after a rebase, is ignored.
	got = Inspect(work, strings.Repeat("0", 40))
	if got.From != fork {
		t.Errorf("unknown base: from = %q, want %q", got.From, fork)
	}
}

func TestInspectOutsideACheckout(t *testing.T) {
	dir := t.TempDir()
	if got := Inspect(dir, ""); got != nil {
		t.Errorf("plain folder = %+v", *got)
	}
	if got := Inspect(filepath.Join(dir, "missing"), ""); got != nil {
		t.Errorf("missing folder = %+v", *got)
	}
	if got := Inspect("", ""); got != nil {
		t.Errorf("no folder = %+v", *got)
	}
	remote, work := clone(t)
	if got := Inspect(remote, ""); got != nil {
		t.Errorf("bare repository = %+v", *got)
	}
	// A checkout set to bare by mistake has no status: it holds no change rather than one never committed.
	run(t, work, "config", "core.bare", "true")
	if got := Inspect(work, ""); got != nil {
		t.Errorf("checkout set to bare = %+v", *got)
	}
}

func TestUnshipped(t *testing.T) {
	remote, work := clone(t)
	check := func(what string, want bool) {
		t.Helper()
		if got := Unshipped(work); got != want {
			t.Errorf("%s: unshipped = %v, want %v", what, got, want)
		}
	}
	check("pushed", false)
	os.WriteFile(filepath.Join(work, "c"), []byte("new"), 0o644)
	check("a file git does not track", false)
	os.WriteFile(filepath.Join(work, "a"), []byte("changed"), 0o644)
	check("a changed file", true)
	run(t, work, "checkout", "-q", "--", "a")
	commit(t, work, "b", "x\n")
	check("a commit not pushed", true)
	run(t, work, "push", "-q")
	check("pushed again", false)

	// A branch ships once the default branch holds its commits, whatever the branch itself.
	run(t, work, "checkout", "-q", "-b", "feature")
	check("a branch with no commit of its own", false)
	commit(t, work, "d", "y\n")
	run(t, work, "push", "-q", "-u", "origin", "feature")
	check("a branch pushed for a pull request", true)
	run(t, work, "push", "-q", "origin", "HEAD:main")
	run(t, work, "fetch", "-q")
	check("a branch merged into main", false)

	// Without a default branch on origin, the upstream is what a change ships to.
	run(t, work, "update-ref", "-d", "refs/remotes/origin/main")
	run(t, work, "update-ref", "-d", "refs/remotes/origin/HEAD")
	check("a branch even with its upstream", false)
	commit(t, work, "e", "z\n")
	check("a commit its upstream lacks", true)

	// A remote never pushed to: the commits have nowhere they shipped.
	local := filepath.Join(t.TempDir(), "local")
	run(t, filepath.Dir(local), "init", "-q", "-b", "main", local)
	if Unshipped(local) {
		t.Error("an empty repository holds a change")
	}
	commit(t, local, "a", "one\n")
	if Unshipped(local) {
		t.Error("a repository with no remote has nothing to ship to")
	}
	run(t, local, "remote", "add", "origin", remote)
	if !Unshipped(local) {
		t.Error("commits never pushed to the remote are not a change")
	}

	if Unshipped(remote) || Unshipped(filepath.Join(t.TempDir(), "gone")) || Unshipped(t.TempDir()) {
		t.Error("a bare, missing or plain folder holds no change")
	}
}
