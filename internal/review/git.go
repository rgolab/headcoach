package review

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Change is what a task's checkout holds: the commits since the task started, and what is left to push.
type Change struct {
	Branch string `json:"branch"`
	// Default tells the branch is the repository's default one, where a change is pushed rather than proposed.
	Default bool `json:"default"`
	// From and To are the commits the change spans, From excluded. From is empty when nothing to compare to is known.
	From    string `json:"from"`
	To      string `json:"to"`
	Commits int    `json:"commits"`
	Files   int    `json:"files"`
	Added   int    `json:"added"`
	Deleted int    `json:"deleted"`
	// Unpushed counts the commits the upstream branch lacks, -1 when the branch has no upstream.
	Unpushed int `json:"unpushed"`
	// Dirty tells the checkout has changes not committed yet.
	Dirty bool `json:"dirty"`
}

// Range is the change as git writes a range of commits. Empty when there is nothing to compare to.
func (c Change) Range() string {
	if c.From == "" {
		return ""
	}
	return short(c.From) + ".." + short(c.To)
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// gitRun runs one git command in dir and reports its output and whether it succeeded.
func gitRun(dir string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir, "--no-optional-locks"}, args...)...).Output()
	return strings.TrimSpace(string(out)), err == nil
}

func gitOut(dir string, args ...string) string {
	out, _ := gitRun(dir, args...)
	return out
}

func count(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

var shortstatRe = regexp.MustCompile(`(\d+) (file|insertion|deletion)`)

// defaultBranch is the name of the repository's default branch, as origin knows it.
func defaultBranch(dir string) string {
	if ref := gitOut(dir, "symbolic-ref", "--short", "-q", "refs/remotes/origin/HEAD"); ref != "" {
		return strings.TrimPrefix(ref, "origin/")
	}
	for _, name := range []string{"main", "master"} {
		if _, ok := gitRun(dir, "rev-parse", "--verify", "-q", "refs/heads/"+name); ok {
			return name
		}
	}
	return ""
}

// worktree tells dir is a git checkout with files: not a missing folder, not a bare repository.
func worktree(dir string) bool {
	if info, err := os.Stat(dir); dir == "" || err != nil || !info.IsDir() {
		return false
	}
	return gitOut(dir, "rev-parse", "--is-inside-work-tree") == "true"
}

// Inspect reads the change in the checkout at dir. base is the commit the task started from, empty when
// unknown. Nil when dir is not a git checkout.
func Inspect(dir, base string) *Change {
	if !worktree(dir) {
		return nil
	}
	head, ok := gitRun(dir, "rev-parse", "--verify", "-q", "HEAD")
	if !ok {
		return nil
	}
	c := &Change{To: head, Unpushed: -1}
	c.Branch = gitOut(dir, "branch", "--show-current")
	def := defaultBranch(dir)
	c.Default = c.Branch != "" && c.Branch == def
	switch {
	case base != "" && isAncestor(dir, base, head):
		c.From = base
	case !c.Default && def != "":
		c.From = gitOut(dir, "merge-base", "HEAD", "origin/"+def)
	}
	if c.From == "" {
		c.From = gitOut(dir, "rev-parse", "--verify", "-q", "@{upstream}")
	}
	if c.From != "" {
		c.Commits = count(gitOut(dir, "rev-list", "--count", c.From+"..HEAD"))
		for _, m := range shortstatRe.FindAllStringSubmatch(gitOut(dir, "diff", "--shortstat", c.From, "HEAD"), -1) {
			switch m[2] {
			case "file":
				c.Files = count(m[1])
			case "insertion":
				c.Added = count(m[1])
			case "deletion":
				c.Deleted = count(m[1])
			}
		}
	}
	if out, ok := gitRun(dir, "rev-list", "--count", "@{upstream}..HEAD"); ok {
		c.Unpushed = count(out)
	}
	// Files git does not track are left out: notes and builds lying around are not part of a change.
	// A status that fails counts as dirty, so a change is never taken for shipped on a guess.
	status, ok := gitRun(dir, "status", "--porcelain", "--untracked-files=no")
	c.Dirty = !ok || status != ""
	return c
}

// Unshipped tells the checkout at dir holds a change that has not shipped: files not committed, or
// commits the default branch on origin lacks, or its upstream when origin has no default branch.
// With a remote but nothing of it to compare to, the commits have never been pushed: they count.
// A removed checkout or a bare repository holds none. A status that fails counts as a change.
func Unshipped(dir string) bool {
	if !worktree(dir) {
		return false
	}
	if status, ok := gitRun(dir, "status", "--porcelain", "--untracked-files=no"); !ok || status != "" {
		return true
	}
	ref := "@{upstream}"
	if def := defaultBranch(dir); def != "" {
		if _, ok := gitRun(dir, "rev-parse", "--verify", "-q", "refs/remotes/origin/"+def); ok {
			ref = "refs/remotes/origin/" + def
		}
	}
	if out, ok := gitRun(dir, "rev-list", "--count", ref+"..HEAD"); ok {
		return count(out) > 0
	}
	_, head := gitRun(dir, "rev-parse", "--verify", "-q", "HEAD")
	return head && gitOut(dir, "remote") != ""
}

func isAncestor(dir, commit, head string) bool {
	_, ok := gitRun(dir, "merge-base", "--is-ancestor", commit, head)
	return ok
}

// cacheFor is how long the change of a checkout is reused: the queue polls, git need not run each time.
const cacheFor = 10 * time.Second

type cached struct {
	change *Change
	at     time.Time
}

var (
	cacheMu sync.Mutex
	cache   = map[string]cached{}
)

// InspectCached is Inspect, reusing what it found for the same checkout and base for a few seconds.
func InspectCached(dir, base string) *Change {
	key := dir + "\x00" + base
	cacheMu.Lock()
	hit, ok := cache[key]
	cacheMu.Unlock()
	if ok && time.Since(hit.at) < cacheFor {
		return hit.change
	}
	change := Inspect(dir, base)
	now := time.Now()
	cacheMu.Lock()
	for k, old := range cache {
		if now.Sub(old.at) >= cacheFor {
			delete(cache, k)
		}
	}
	cache[key] = cached{change, now}
	cacheMu.Unlock()
	return change
}
