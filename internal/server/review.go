package server

import (
	"database/sql"
	"log"
	"math"
	"net/http"
	"regexp"
	"sort"

	"github.com/humblebundledore/headcoach/internal/api"
	"github.com/humblebundledore/headcoach/internal/review"
	"github.com/humblebundledore/headcoach/internal/store"
	"github.com/humblebundledore/headcoach/internal/usage"
)

// metricsDays is the period the review metrics cover.
const metricsDays = 7

// queued is what the review queue reads from the board for one task, before it looks at git and transcripts.
type queued struct {
	item        api.ReviewItem
	transcripts []string // the task's sessions and their sub-agents
	reviewer    string   // the transcript of its last reviewer sub-agent, empty when none ran
}

// reviewQueue answers GET /api/review. It locks only while it reads the board: git, transcripts and
// the forge take time. A task whose change has shipped moves to Done on the way: pushed to the default
// branch, or merged through a pull request it opened.
func (s *Server) reviewQueue(r *http.Request) (any, error) {
	s.mu.Lock()
	list, metrics, err := s.reviewBoard()
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	tests, err := review.TestMatcher(s.cfg.TestPattern)
	if err != nil {
		log.Printf("test_pattern: %v: no test run is shown", err)
	}
	out := api.Review{Items: []api.ReviewItem{}, Metrics: metrics}
	var delivered []store.Task
	for _, q := range list {
		item := q.item
		sig := review.OfTask(q.transcripts, tests)
		item.Tests, item.TestCmd = sig.Tests, sig.TestCmd
		if q.reviewer != "" {
			item.Verdict = review.Verdict(review.Of(q.reviewer, nil).LastText)
		}
		item.PR = sig.PR(item.Remote)
		item.Change = s.Inspect(item.Cwd, item.Base)
		c := item.Change
		// A link the task only mentions, on the default branch, may be anything: a push still ships there.
		opened := item.PR != "" && sig.OpenedPR(item.PR)
		item.Delivery = delivery(item.PR != "" && (opened || c == nil || !c.Default), c)
		if item.Delivery != "pr" {
			item.PR = ""
		}
		// Pushed by the task, with nothing left to push or commit: it has shipped.
		pushed := item.Delivery == "push" && sig.Pushed && c.Unpushed == 0 && !c.Dirty
		merged := false
		if item.Delivery == "pr" {
			pr := s.PRState(item.PR)
			item.PRState = pr.State
			// A link the task only mentions may be someone else's: it must have opened it, or the pull
			// request must hold its commits.
			merged = pr.State == "merged" && (opened || review.Holds(item.Cwd, c, pr))
		}
		if pushed || merged {
			delivered = append(delivered, item.Task.Task)
			continue
		}
		out.Items = append(out.Items, item)
	}
	if len(delivered) == 0 {
		return out, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, was := range delivered {
		now, err := s.st.Task(was.ID)
		if err != nil {
			return nil, err
		}
		// Whatever happened to the task since it was read wins.
		if now == nil || now.State != "review" || now.UpdatedAt != was.UpdatedAt {
			continue
		}
		if _, err := s.st.SetState(was.ID, "done", "delivered", nil); err != nil {
			return nil, err
		}
	}
	out.Metrics, err = s.reviewMetrics(store.Now())
	return out, err
}

// delivery tells how a change ships, from what the task did: see api.ReviewItem.
func delivery(pr bool, c *review.Change) string {
	switch {
	case pr:
		return "pr"
	case c == nil || (c.Commits == 0 && !c.Dirty):
		return ""
	case c.Default:
		return "push"
	case c.Branch != "":
		return "branch"
	}
	return ""
}

// reviewBoard reads the tasks in Review, the one waiting longest first, and the metrics.
func (s *Server) reviewBoard() ([]queued, api.ReviewMetrics, error) {
	now := store.Now()
	metrics, err := s.reviewMetrics(now)
	if err != nil {
		return nil, metrics, err
	}
	tasks, err := s.st.Tasks("WHERE state = 'review'")
	if err != nil {
		return nil, metrics, err
	}
	var out []queued
	for i := range tasks {
		task := &tasks[i]
		view, err := s.view(task, false)
		if err != nil {
			return nil, metrics, err
		}
		q := queued{item: api.ReviewItem{Task: *view}}
		var since sql.NullFloat64
		err = s.st.QueryRow("SELECT max(at) FROM history WHERE task_id = ? AND state = 'review'", task.ID).Scan(&since)
		if err != nil {
			return nil, metrics, err
		}
		if since.Valid {
			q.item.Waited = int(now - since.Float64)
		}
		sessions, err := s.st.TaskSessions(task.ID)
		if err != nil {
			return nil, metrics, err
		}
		// Sub-agents first: a link the session gives wins over one a sub-agent mentions.
		agentFiles := map[string]string{}
		for _, path := range transcripts(sessions) {
			for id, file := range usage.AgentFiles(path) {
				agentFiles[id] = file
				q.transcripts = append(q.transcripts, file)
			}
		}
		q.transcripts = append(q.transcripts, transcripts(sessions)...)
		if q.reviewer, err = s.lastReviewer(task.ID, agentFiles); err != nil {
			return nil, metrics, err
		}
		out = append(out, q)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].item.Waited > out[j].item.Waited })
	return out, metrics, nil
}

// reviewerRe matches the type of a sub-agent that reviews: reviewer, code-reviewer, pr_reviewer.
var reviewerRe = regexp.MustCompile(`(^|[-_:])reviewer($|[-_])`)

// lastReviewer is the transcript of the last reviewer sub-agent of a task that finished: one that loaded
// a reviewer persona, or whose type is a reviewer. Empty when none did.
func (s *Server) lastReviewer(taskID string, files map[string]string) (string, error) {
	rows, err := s.st.Query("SELECT agent_id, name, persona FROM agents WHERE task_id = ? AND agent_id IS NOT NULL"+
		" AND state != 'running' ORDER BY id DESC", taskID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name, persona string
		if err := rows.Scan(&id, &name, &persona); err != nil {
			return "", err
		}
		if (reviewerRe.MatchString(persona) || reviewerRe.MatchString(name)) && files[id] != "" {
			return files[id], nil
		}
	}
	return "", rows.Err()
}

// reviewMetrics is how long tasks waited in Review, and what a delivered change cost, over the last days.
func (s *Server) reviewMetrics(now float64) (api.ReviewMetrics, error) {
	m := api.ReviewMetrics{Days: metricsDays}
	since := now - metricsDays*86400
	var wait float64
	err := s.st.QueryRow(
		"SELECT count(*), coalesce(avg(left_at - at), 0) FROM ("+
			" SELECT state, at, lead(at) OVER (PARTITION BY task_id ORDER BY id) AS left_at FROM history)"+
			" WHERE state = 'review' AND left_at >= ?", since,
	).Scan(&m.Reviews, &wait)
	if err != nil {
		return m, err
	}
	m.AvgWait = int(wait)
	var cost float64
	err = s.st.QueryRow(
		"SELECT count(*), coalesce(avg((SELECT coalesce(sum(cost), 0) FROM sessions s WHERE s.task_id = t.id)), 0)"+
			" FROM tasks t WHERE t.state = 'done'"+
			" AND (SELECT max(at) FROM history h WHERE h.task_id = t.id AND h.state = 'done') >= ?", since,
	).Scan(&m.Delivered, &cost)
	m.CostPerChange = math.Round(cost*100) / 100
	return m, err
}
