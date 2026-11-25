package server

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rouxlabs/headcoach/internal/api"
	"github.com/rouxlabs/headcoach/internal/format"
	"github.com/rouxlabs/headcoach/internal/review"
)

// gitIn runs git in dir, with no global or system config so the machine's settings do not leak in.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=T", "-c", "user.email=t@example.com",
		"-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func gitCommit(t *testing.T, dir, file string) {
	t.Helper()
	os.WriteFile(filepath.Join(dir, file), []byte(file+"\n"), 0o644)
	gitIn(t, dir, "add", file)
	gitIn(t, dir, "commit", "-q", "-m", file)
}

// checkout makes a remote on github.com's address scheme and a clone of it with one pushed commit on main.
func (f *fixture) checkout() string {
	f.t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		f.t.Skip("git is not installed")
	}
	f.t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	f.t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	remote, work := filepath.Join(f.tmp, "remote.git"), filepath.Join(f.tmp, "repo")
	gitIn(f.t, f.tmp, "init", "-q", "--bare", "-b", "main", remote)
	gitIn(f.t, f.tmp, "clone", "-q", remote, work)
	gitIn(f.t, work, "checkout", "-q", "-b", "main")
	gitCommit(f.t, work, "a")
	gitIn(f.t, work, "push", "-q", "-u", "origin", "main")
	gitIn(f.t, work, "remote", "set-head", "origin", "main")
	f.srv.Inspect = review.Inspect // each look sees the commits just made
	return work
}

func line(t *testing.T, v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw) + "\n"
}

// appendTo adds to a transcript the way Claude Code does: transcripts only grow.
func appendTo(t *testing.T, path, text string) {
	t.Helper()
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	if _, err := fh.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) review() api.Review {
	f.t.Helper()
	var out api.Review
	if err := json.Unmarshal(f.ok(f.get("/api/review")).raw, &out); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func TestReviewQueue(t *testing.T) {
	f := newFixture(t)
	work := f.checkout()
	eq(t, len(f.review().Items), 0)

	// s1 commits on main, runs the tests, has a reviewer sub-agent and pushes.
	main := filepath.Join(f.tmp, "s1.jsonl")
	f.write("s1.jsonl", "")
	f.hook("UserPromptSubmit", "s1", obj{"prompt": "fix it", "cwd": work, "transcript_path": main})
	gitCommit(t, work, "b")
	gitCommit(t, work, "c")
	f.hook("SubagentStart", "s1", obj{"agent_id": "r1", "agent_type": "general-purpose"})
	f.hook("PreToolUse", "s1", obj{"agent_id": "r1", "tool_name": "Skill", "tool_input": obj{"skill": "reviewer"}})
	f.hook("SubagentStop", "s1", obj{"agent_id": "r1"})
	f.write("s1/subagents/agent-r1.jsonl", line(t, obj{"type": "assistant", "message": obj{"content": []obj{
		{"type": "text", "text": "# Review\n\nVerdict: approve, no blocking findings"}}}}))
	f.write("s1.jsonl", line(t, obj{"type": "assistant", "timestamp": "2026-01-01T10:00:00Z", "message": obj{"content": []obj{
		{"type": "tool_use", "id": "t1", "name": "Bash", "input": obj{"command": "make check"}}}}})+
		line(t, obj{"type": "user", "timestamp": "2026-01-01T10:01:00Z", "message": obj{"content": []obj{
			{"type": "tool_result", "tool_use_id": "t1", "content": "ok"}}}}))
	f.hook("Stop", "s1", obj{"last_assistant_message": "Done."})

	// s2 is a plan in a plain folder, in Review for longer.
	f.hook("UserPromptSubmit", "s2", obj{"prompt": "/architect sketch the cache", "cwd": f.tmp}, "X-Tmux-Pane", "%2")
	f.hook("Stop", "s2", obj{"last_assistant_message": "Here."}, "X-Tmux-Pane", "%2")
	f.st.Exec("UPDATE history SET at = at - 3600 WHERE task_id = 's2' AND state = 'review'")

	queue := f.review()
	if len(queue.Items) != 2 || queue.Items[0].ID != "s2" || queue.Items[1].ID != "s1" {
		t.Fatalf("queue = %+v", queue.Items)
	}
	plain := queue.Items[0]
	if plain.Waited < 3600 || plain.Change != nil || plain.Delivery != "" || plain.Tests != "" {
		t.Errorf("plain folder = %+v", plain)
	}
	it := queue.Items[1]
	c := it.Change
	if c == nil || !c.Default || c.Commits != 2 || c.Files != 2 || c.Added != 2 || c.Unpushed != 2 || c.Dirty {
		t.Fatalf("change = %+v", c)
	}
	eq(t, it.Tests, "passed")
	eq(t, it.TestCmd, "make check")
	eq(t, it.Verdict, "Verdict: approve, no blocking findings")
	eq(t, it.Delivery, "push")

	// The push the session ran, and its commits on the remote.
	gitIn(t, work, "push", "-q")
	appendTo(t, main, line(t, obj{"type": "assistant", "timestamp": "2026-01-01T10:02:00Z", "message": obj{"content": []obj{
		{"type": "tool_use", "id": "p1", "name": "Bash", "input": obj{"command": "git push"}}}}})+
		line(t, obj{"type": "user", "timestamp": "2026-01-01T10:03:00Z", "message": obj{"content": []obj{
			{"type": "tool_result", "tool_use_id": "p1", "content": "main -> main"}}}}))
	// Delivered: the task moves to Done and leaves the queue.
	if queue := f.review(); len(queue.Items) != 1 || queue.Items[0].ID != "s2" {
		t.Errorf("after the push = %+v", queue.Items)
	}
	eq(t, f.history("s1"), []string{"running hook", "review hook", "done delivered"})
}

func TestReviewQueueLeavesAPushThatIsNotDone(t *testing.T) {
	f := newFixture(t)
	work := f.checkout()
	main := f.write("s1.jsonl", "")
	f.hook("UserPromptSubmit", "s1", obj{"prompt": "fix it", "cwd": work, "transcript_path": main})
	gitCommit(t, work, "b")
	f.hook("Stop", "s1", obj{"last_assistant_message": "Done."})
	gitIn(t, work, "push", "-q")
	// Pushed, but not by the task: you did, or another task did.
	if it := f.review().Items; len(it) != 1 || format.DeliveryCell(it[0]) != "pushed" {
		t.Errorf("a push the task did not run = %+v", it)
	}
	appendTo(t, main, line(t, obj{"type": "assistant", "timestamp": "2026-01-01T10:02:00Z", "message": obj{"content": []obj{
		{"type": "tool_use", "id": "p1", "name": "Bash", "input": obj{"command": "git push"}}}}})+
		line(t, obj{"type": "user", "timestamp": "2026-01-01T10:03:00Z", "message": obj{"content": []obj{
			{"type": "tool_result", "tool_use_id": "p1", "content": "main -> main"}}}}))
	// Changes left to commit keep it in Review.
	os.WriteFile(filepath.Join(work, "a"), []byte("changed"), 0o644)
	if it := f.review().Items; len(it) != 1 {
		t.Errorf("a push with changes left = %+v", it)
	}
	gitIn(t, work, "checkout", "--", "a")
	// So does activity since the queue read it: the task went back to work.
	f.srv.Inspect = func(dir, base string) *review.Change {
		f.hook("UserPromptSubmit", "s1", obj{"prompt": "one more thing"})
		return review.Inspect(dir, base)
	}
	f.review()
	eq(t, f.task("s1").State, "running")
}

func TestReviewQueueOnABranchWithAPullRequest(t *testing.T) {
	f := newFixture(t)
	work := f.checkout()
	f.write("s1.jsonl", "")
	f.hook("UserPromptSubmit", "s1", obj{"prompt": "feature", "cwd": work, "transcript_path": filepath.Join(f.tmp, "s1.jsonl")})
	gitIn(t, work, "checkout", "-q", "-b", "feature")
	gitCommit(t, work, "b")
	f.hook("Stop", "s1", obj{"last_assistant_message": "Done."})
	it := f.review().Items[0]
	if it.Delivery != "branch" || it.Change.Branch != "feature" || it.Change.Commits != 1 || it.PR != "" {
		t.Errorf("branch without a PR = %+v %+v", it, it.Change)
	}
	appendTo(t, filepath.Join(f.tmp, "s1.jsonl"), line(t, obj{"type": "assistant", "message": obj{"content": []obj{
		{"type": "text", "text": "Opened https://github.com/o/r/pull/12"}}}}))
	f.prs["https://github.com/o/r/pull/12"] = review.PR{State: "open"}
	if it := f.review().Items[0]; it.Delivery != "pr" || it.PR != "https://github.com/o/r/pull/12" || it.PRState != "open" {
		t.Errorf("branch with a PR = %+v", it)
	}
	// Merged without the task's commits: a link the task only mentions is not its delivery, even from its branch.
	f.prs["https://github.com/o/r/pull/12"] = review.PR{State: "merged", Head: gitIn(t, work, "rev-parse", "HEAD~1")}
	if it := f.review().Items; len(it) != 1 || it[0].PRState != "merged" {
		t.Errorf("someone else's PR = %+v", it)
	}
	// Merged with the task's commits in it.
	f.prs["https://github.com/o/r/pull/12"] = review.PR{State: "merged", Head: gitIn(t, work, "rev-parse", "HEAD")}
	if it := f.review().Items; len(it) != 0 {
		t.Errorf("a merged PR = %+v", it)
	}
	eq(t, f.task("s1").State, "done")
}

func TestReviewQueueOnMainIgnoresAMentionedPullRequest(t *testing.T) {
	f := newFixture(t)
	work := f.checkout()
	main := f.write("s1.jsonl", "")
	f.hook("UserPromptSubmit", "s1", obj{"prompt": "fix it", "cwd": work, "transcript_path": main})
	gitCommit(t, work, "b")
	f.hook("Stop", "s1", obj{"last_assistant_message": "Ready to push."})
	eq(t, f.task("s1").State, "review")
	gitIn(t, work, "push", "-q")
	appendTo(t, main, line(t, obj{"type": "assistant", "message": obj{"content": []obj{
		{"type": "text", "text": "Same as https://github.com/o/r/pull/12"},
		{"type": "tool_use", "id": "p1", "name": "Bash", "input": obj{"command": "git push"}}}}})+
		line(t, obj{"type": "user", "message": obj{"content": []obj{
			{"type": "tool_result", "tool_use_id": "p1", "content": "main -> main"}}}}))
	if it := f.review().Items; len(it) != 0 {
		t.Errorf("a push to main that mentions a PR = %+v", it)
	}
	eq(t, f.task("s1").State, "done")
}

func TestReviewerIsTheLastReviewerSubAgent(t *testing.T) {
	f := newFixture(t)
	f.write("s1.jsonl", "")
	f.hook("UserPromptSubmit", "s1", obj{"prompt": "x", "cwd": f.tmp, "transcript_path": filepath.Join(f.tmp, "s1.jsonl")})
	for _, a := range []struct{ id, kind, says string }{
		{"r1", "code-reviewer", "Verdict: approve"}, {"r2", "preview", "Verdict: not a reviewer"}} {
		f.hook("SubagentStart", "s1", obj{"agent_id": a.id, "agent_type": a.kind})
		f.hook("SubagentStop", "s1", obj{"agent_id": a.id})
		f.write("s1/subagents/agent-"+a.id+".jsonl", line(t, obj{"type": "assistant", "message": obj{"content": []obj{
			{"type": "text", "text": a.says}}}}))
	}
	f.hook("Stop", "s1", obj{"last_assistant_message": "Done."})
	f.st.SetState("s1", "review", "you", nil) // a plain folder finishes in Done
	eq(t, f.review().Items[0].Verdict, "Verdict: approve")
}

func TestReviewQueueMergedPullRequestTheTaskOpened(t *testing.T) {
	f := newFixture(t)
	work := f.checkout()
	main := f.write("s1.jsonl", "")
	f.hook("UserPromptSubmit", "s1", obj{"prompt": "feature", "cwd": work, "transcript_path": main})
	gitIn(t, work, "checkout", "-q", "-b", "feature")
	gitCommit(t, work, "b")
	appendTo(t, main, line(t, obj{"type": "assistant", "message": obj{"content": []obj{
		{"type": "tool_use", "id": "g1", "name": "Bash", "input": obj{"command": "gh pr create --fill"}}}}})+
		line(t, obj{"type": "user", "message": obj{"content": []obj{
			{"type": "tool_result", "tool_use_id": "g1", "content": "https://github.com/o/r/pull/3\n"}}}}))
	f.hook("Stop", "s1", obj{"last_assistant_message": "Done."})
	gitIn(t, work, "checkout", "-q", "main") // back on main once it is merged
	f.prs["https://github.com/o/r/pull/3"] = review.PR{State: "merged"}
	if it := f.review().Items; len(it) != 0 {
		t.Errorf("a merged PR the task opened = %+v", it)
	}
	eq(t, f.history("s1"), []string{"running hook", "review hook", "done delivered"})
}

func TestReviewMetrics(t *testing.T) {
	f := newFixture(t)
	now := float64(2_000_000_000)
	for _, h := range []struct {
		task, state string
		at          float64
	}{
		{"a", "running", now - 9000}, {"a", "review", now - 8000}, {"a", "running", now - 7000}, // 1000 s in Review
		{"a", "review", now - 6000}, {"a", "done", now - 3000}, // 3000 s in Review, then done
		{"b", "review", now - 30*86400}, {"b", "done", now - 29*86400}, // too old
		{"c", "review", now - 100}, // still waiting
	} {
		f.st.Exec("INSERT INTO history (task_id, state, at) VALUES (?, ?, ?)", h.task, h.state, h.at)
	}
	for _, id := range []string{"a", "b"} {
		f.st.Exec("INSERT INTO tasks (id, title, state, created_at, updated_at) VALUES (?, ?, 'done', 0, 0)", id, id)
	}
	f.st.Exec("INSERT INTO sessions (session_id, task_id, cost, started_at) VALUES ('sa1', 'a', 1.5, 0), ('sa2', 'a', 0.25, 0), ('sb', 'b', 9, 0)")
	m, err := f.srv.reviewMetrics(now)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, m, api.ReviewMetrics{Days: 7, Reviews: 2, AvgWait: 2000, Delivered: 1, CostPerChange: 1.75})
}
