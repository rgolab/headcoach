package cli

import (
	"fmt"

	"github.com/rouxlabs/headcoach/internal/api"
	"github.com/rouxlabs/headcoach/internal/format"
)

// testsStyle is the color of a test result.
var testsStyle = map[string]string{"passed": "ok", "failed": "hot"}

// orDash writes an empty cell as a dash.
func orDash(text string) string {
	if text == "" {
		return "-"
	}
	return text
}

func cmdReview(e *env, p parsed) error {
	var queue api.Review
	if err := e.client.Call("GET", "/api/review", nil, &queue); err != nil {
		return err
	}
	if p.has("json") {
		return e.json(queue)
	}
	if len(queue.Items) == 0 {
		e.printf("Nothing waits on you in Review.\n")
	} else {
		const columns = "%-9s %6s %-16s %-20s %-28s %-7s %-12s %-28s  "
		e.printf("%s\n", e.paint(fmt.Sprintf(columns+"TITLE", "TASK", "WAITED", "REPO", "BRANCH", "CHANGE", "TESTS", "DELIVERY", "REVIEWER"), "dim"))
		for _, it := range queue.Items {
			tests := e.paint(fmt.Sprintf("%-7s", orDash(it.Tests)), testsStyle[it.Tests])
			e.printf("%-9s %6s %-16s %-20s %-28s %s %-12s %-28s  %s\n", it.Short, format.Human(it.Waited),
				cut(orDash(it.Repo), 16), cut(orDash(it.Branch), 20), cut(format.ChangeCell(it.Change), 28), tests,
				format.DeliveryCell(it), cut(orDash(it.Verdict), 28), format.Timed(format.Marked(it.Title, it.Source), it.TimerLeft))
			where := it.PR
			if where == "" && it.Change != nil && it.Change.Commits > 0 {
				where = it.Change.Range()
			}
			if where != "" {
				e.printf("%s\n", e.paint(fmt.Sprintf("%-9s %s", "", where), "dim"))
			}
		}
	}
	e.printf("\n%s\n", e.paint(format.ReviewMetrics(queue.Metrics), "dim"))
	return nil
}
