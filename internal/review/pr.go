package review

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// PR is what the forge says about a pull or merge request.
type PR struct {
	// State is "open", "merged" or "closed". Empty when it could not be asked.
	State string `json:"state"`
	// Head is the last commit on the branch it proposes.
	Head string `json:"head"`
}

// askFor is how long the state of a pull request is reused before the forge is asked again. A merged
// one is not asked again: a closed one can be reopened.
const askFor = 2 * time.Minute

type asked struct {
	pr PR
	at time.Time
}

var (
	prMu    sync.Mutex
	prCache = map[string]asked{}
	// prAsking holds the questions on their way, so that two polls wait for the same answer.
	prAsking = map[string]chan struct{}{}
)

// forge runs gh or glab, when installed. Tests replace it.
var forge = func(name string, args ...string) ([]byte, error) {
	if _, err := exec.LookPath(name); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

// StateOf asks the forge about a pull request, with gh for GitHub and glab for GitLab, when they are
// installed. An empty state means it could not tell.
func StateOf(url string) PR {
	prMu.Lock()
	for {
		hit, ok := prCache[url]
		if ok && (hit.pr.State == "merged" || time.Since(hit.at) < askFor) {
			prMu.Unlock()
			return hit.pr
		}
		wait, asking := prAsking[url]
		if !asking {
			break
		}
		prMu.Unlock()
		<-wait
		prMu.Lock()
	}
	done := make(chan struct{})
	prAsking[url] = done
	prMu.Unlock()
	pr := ask(url)
	prMu.Lock()
	prCache[url] = asked{pr, time.Now()}
	delete(prAsking, url)
	prMu.Unlock()
	close(done)
	return pr
}

func ask(url string) PR {
	var pr PR
	if strings.Contains(url, "/-/merge_requests/") {
		out, err := forge("glab", "mr", "view", url, "--output", "json")
		var mr struct {
			State string `json:"state"`
			SHA   string `json:"sha"`
		}
		if err != nil || json.Unmarshal(out, &mr) != nil {
			return PR{}
		}
		pr = PR{State: mr.State, Head: mr.SHA}
	} else {
		out, err := forge("gh", "pr", "view", url, "--json", "state,headRefOid")
		var gh struct {
			State string `json:"state"`
			Oid   string `json:"headRefOid"`
		}
		if err != nil || json.Unmarshal(out, &gh) != nil {
			return PR{}
		}
		pr = PR{State: gh.State, Head: gh.Oid}
	}
	pr.State = strings.ToLower(pr.State)
	if pr.State == "opened" { // GitLab's word
		pr.State = "open"
	}
	return pr
}

// Holds tells whether a pull request carries the commits of a change: its last commit is one the
// pull request's head contains. False when the head is unknown or not in the checkout.
func Holds(dir string, c *Change, pr PR) bool {
	if c == nil || c.Commits == 0 || pr.Head == "" {
		return false
	}
	return isAncestor(dir, c.To, pr.Head)
}
