package server

import (
	"net/http"

	"github.com/rouxlabs/headcoach/internal/api"
	"github.com/rouxlabs/headcoach/internal/board"
	"github.com/rouxlabs/headcoach/internal/format"
	"github.com/rouxlabs/headcoach/internal/store"
)

// team answers with what a coordinator looks at: the tasks under it at any depth, in the order of the
// board, and what waits on you among them and itself. A hidden task is left out, with what is under it.
func (s *Server) team(r *http.Request) (any, error) {
	root, err := s.find(r)
	if err != nil {
		return nil, err
	}
	linked, err := s.st.Tasks("WHERE parent != '' AND hidden = 0")
	if err != nil {
		return nil, err
	}
	under := map[string][]store.Task{}
	for _, t := range linked {
		under[t.Parent] = append(under[t.Parent], t)
	}
	var views []api.Task
	seen := map[string]bool{root.ID: true}
	for queue := []string{root.ID}; len(queue) > 0; queue = queue[1:] {
		for i := range under[queue[0]] {
			t := &under[queue[0]][i]
			if seen[t.ID] {
				continue
			}
			seen[t.ID] = true
			view, err := s.view(t, false)
			if err != nil {
				return nil, err
			}
			views, queue = append(views, *view), append(queue, t.ID)
		}
	}
	top, err := s.view(root, false)
	if err != nil {
		return nil, err
	}
	out := api.Team{Task: *top, Handoff: s.readHandoff(root.ID).Summary, Members: []api.Member{}, OnYou: []api.OnYou{}}
	out.OnYou = onYou(out.OnYou, top, out.Handoff.OnYou())
	for _, row := range format.TreeRows(views, "state") {
		if row.Agent != nil {
			continue
		}
		h := s.readHandoff(row.Task.ID)
		out.Members = append(out.Members, api.Member{Task: *row.Task, Depth: row.Depth, Tree: row.Tree, Kids: row.Kids,
			Handoff: h.Summary, HandoffAt: h.UpdatedAt})
		out.OnYou = onYou(out.OnYou, row.Task, h.Summary.OnYou())
	}
	return out, nil
}

// onYou adds what waits on you in one task: its question, its change in Review, and the lines of its
// handoff that say so.
func onYou(list []api.OnYou, t *api.Task, handoff []string) []api.OnYou {
	add := func(kind, what string) {
		list = append(list, api.OnYou{Task: t.ID, Short: board.Short(t.ID), Title: t.Title, Kind: kind, What: what})
	}
	switch t.State {
	case "needs":
		add("needs", t.Ask)
	case "review":
		add("review", t.Last)
	}
	for _, what := range handoff {
		add("handoff", what)
	}
	return list
}
