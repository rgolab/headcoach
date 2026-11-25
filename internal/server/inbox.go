package server

import (
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/rouxlabs/headcoach/internal/api"
	"github.com/rouxlabs/headcoach/internal/board"
	"github.com/rouxlabs/headcoach/internal/store"
)

// A parent task hears from its children without asking: when a child stops in Needs me, Review, Done
// or Parked, the board types a short message into the parent's session. It waits until the parent can
// hear it, so it never cuts into a turn, a dialog or a question to you.
const (
	inboxEvery  = 5 * time.Second
	inboxSettle = 10.0 // seconds a child stays put before its parent hears of it: a passing state is no news
	inboxGap    = 60.0 // seconds at least between two messages to the same parent
)

// RunInbox delivers what children have to say to their parents, until stop is closed.
func (s *Server) RunInbox(stop <-chan struct{}) {
	tick := time.NewTicker(inboxEvery)
	defer tick.Stop()
	for {
		if err := s.Deliver(store.Now()); err != nil {
			log.Printf("inbox: %v", err)
		}
		select {
		case <-stop:
			return
		case <-tick.C:
		}
	}
}

// Deliver types into each parent's session what its children have to say, when the parent can hear it.
func (s *Server) Deliver(now float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending, err := s.st.Inbox("WHERE delivered_at = 0 ORDER BY id")
	if err != nil {
		return err
	}
	var parents []string
	byParent := map[string][]store.InboxItem{}
	for _, it := range pending {
		if byParent[it.ParentID] == nil {
			parents = append(parents, it.ParentID)
		}
		byParent[it.ParentID] = append(byParent[it.ParentID], it)
	}
	for _, id := range parents {
		if err := s.deliverTo(id, byParent[id], now); err != nil {
			return err
		}
	}
	return nil
}

// deliverTo sends one parent, in one message, what its children have to say.
func (s *Server) deliverTo(parentID string, items []store.InboxItem, now float64) error {
	parent, err := s.st.Task(parentID)
	if err != nil {
		return err
	}
	// What no longer holds is dropped: the child is gone, under another parent, or moved on since.
	var news []store.InboxItem
	children := map[string]*store.Task{}
	for _, it := range items {
		child, err := s.st.Task(it.ChildID)
		if err != nil {
			return err
		}
		if parent == nil || child == nil || child.Parent != parentID || child.State != it.State {
			if err := s.st.DropInbox(it.ID); err != nil {
				return err
			}
			continue
		}
		if now-it.At >= inboxSettle {
			news, children[child.ID] = append(news, it), child
		}
	}
	if len(news) == 0 {
		return nil
	}
	if last, err := s.st.LastDelivery(parentID); err != nil || now-last < inboxGap {
		return err
	}
	sess, err := s.listening(parent)
	if err != nil || sess == nil {
		return err
	}
	text := inboxText(news, children)
	if err := s.tmux.SendText(sess.Pane, text, sess.Socket); err != nil {
		log.Printf("inbox: %s: %v", board.Short(parentID), err)
		return nil // it stays waiting: the next tick tries again
	}
	ids := make([]int64, len(news))
	for i := range news {
		ids[i] = news[i].ID
	}
	if err := s.st.Delivered(parentID, ids, now); err != nil {
		return err
	}
	if err := s.record(parent, sender{kind: board.FromHC}, "inbox", text); err != nil {
		return err
	}
	return s.st.UpdateTask(parentID, store.Fields{"last": board.Said(text)})
}

// listening returns the parent's live session when the board may type into it: its turn is over, or
// it only waits on its own jobs, and its prompt is on screen and empty. Nil when the parent cannot hear
// now: it is working, it waits on you, it shows a dialog, words wait in its prompt, or it has no session.
func (s *Server) listening(task *store.Task) (*store.Session, error) {
	idle := task.State == "done" || task.State == "review" || task.State == "running" && task.Waiting
	if !idle {
		return nil, nil
	}
	sess, err := s.liveSession(task.ID)
	if err != nil || sess == nil {
		return nil, err
	}
	typed, shown := promptBox(s.tmux.CaptureStyled(sess.Pane, sess.Socket))
	if !shown || typed != "" {
		return nil, nil
	}
	return sess, nil
}

var (
	// ruleRe is a line of the rules Claude Code draws above and below its prompt.
	ruleRe = regexp.MustCompile(`^─{10,}$`)
	// escRe is an escape sequence of a styled capture: a color or a style, or a link.
	escRe = regexp.MustCompile(`\x1b\[[0-9;:]*m|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	// sgrRe is a color or a style at the start of styled text, with its parameters.
	sgrRe = regexp.MustCompile(`^\x1b\[([0-9;:]*)m`)
)

// promptBox finds Claude Code's prompt at the bottom of a styled screen: the lines between the last two
// rules, the first one starting with its prompt sign. It returns what is typed there, and whether the
// prompt is on screen at all; a dialog takes its place. A suggestion or a placeholder Claude Code
// draws in the prompt, dim, is nothing typed.
func promptBox(screen string) (typed string, shown bool) {
	styled := strings.Split(screen, "\n")
	lines := make([]string, len(styled))
	for i, line := range styled {
		// Claude Code puts a no-break space after its prompt sign.
		lines[i] = strings.ReplaceAll(escRe.ReplaceAllString(line, ""), "\u00a0", " ")
	}
	var rules []int
	for i := len(lines) - 1; i >= 0 && len(rules) < 2; i-- {
		if ruleRe.MatchString(strings.TrimSpace(lines[i])) {
			rules = append(rules, i)
		}
	}
	if len(rules) < 2 || rules[0]-rules[1] < 2 {
		return "", false
	}
	inside := lines[rules[1]+1 : rules[0]]
	first := strings.TrimSpace(inside[0])
	sign := ""
	for _, prompt := range []string{"❯", ">"} {
		if first == prompt || strings.HasPrefix(first, prompt+" ") {
			sign = prompt
		}
	}
	if sign == "" || optionRe.MatchString(first) {
		return "", false
	}
	if _, after, _ := strings.Cut(styled[rules[1]+1], sign); dimStart(after) {
		return "", true
	}
	words := []string{strings.TrimPrefix(first, sign)}
	for _, line := range inside[1:] {
		words = append(words, line)
	}
	return strings.TrimSpace(strings.Join(strings.Fields(strings.Join(words, " ")), " ")), true
}

// dimStart tells whether the first thing styled text draws is dim.
func dimStart(styled string) bool {
	dim := false
	for styled != "" {
		if found := sgrRe.FindStringSubmatch(styled); found != nil {
			dim = sgrDim(found[1], dim)
			styled = styled[len(found[0]):]
			continue
		}
		r, size := utf8.DecodeRuneInString(styled)
		if !unicode.IsSpace(r) {
			return dim
		}
		styled = styled[size:]
	}
	return false
}

// sgrDim applies the parameters of one color or style sequence to whether the text is dim.
func sgrDim(params string, dim bool) bool {
	list := strings.Split(params, ";")
	for i := 0; i < len(list); i++ {
		switch list[i] {
		case "2":
			dim = true
		case "", "0", "22":
			dim = false
		case "38", "48", "58": // a color of 256 or of 16 million: its arguments are no style
			if i+1 < len(list) && list[i+1] == "5" {
				i += 2
			} else if i+1 < len(list) && list[i+1] == "2" {
				i += 4
			}
		}
	}
	return dim
}

// inboxText is the message a parent gets: one line per child, under a label that says it is not you.
func inboxText(items []store.InboxItem, children map[string]*store.Task) string {
	lines := []string{board.Label(board.FromHC, "", false) + "Updates from the tasks you drive, not from your user:"}
	for _, it := range items {
		lines = append(lines, fmt.Sprintf("- %s %q %s", board.Short(it.ChildID), board.Clip(children[it.ChildID].Title, 60), news(it)))
	}
	return strings.Join(append(lines, "hc show <task> tells more."), "\n")
}

// news says in a few words what a child's new state means for its parent.
func news(it store.InboxItem) string {
	switch it.State {
	case "needs":
		switch it.AskKind {
		case "question":
			return "asks: " + board.Clip(it.Ask, 200)
		case "permission":
			return "waits at a permission prompt: " + board.Clip(it.Ask, 200)
		}
		return "waits at a dialog: " + board.Clip(it.Ask, 200)
	case "review":
		return "is in Review: " + board.Clip(toAgent(it.Last), 200)
	case "done":
		return "is Done: " + board.Clip(toAgent(it.Last), 200)
	}
	return "is " + strings.ToLower(store.StateName(it.State))
}

// byYouRe is a line the board writes when you move a task.
var byYouRe = regexp.MustCompile(`^(Moved|Parked) by you$`)

// toAgent says a line of the board to an agent, for whom you is the agent itself: a move you made
// is one its user made.
func toAgent(last string) string { return byYouRe.ReplaceAllString(last, "$1 by your user") }

// inboxShown is how many items hc inbox shows.
const inboxShown = 50

// inbox answers with what a task heard from its children, and what waits to be said: newest first.
func (s *Server) inbox(r *http.Request) (any, error) {
	task, err := s.find(r)
	if err != nil {
		return nil, err
	}
	items, err := s.st.Inbox("WHERE parent_id = ? ORDER BY delivered_at = 0 DESC, id DESC LIMIT ?", task.ID, inboxShown)
	if err != nil {
		return nil, err
	}
	out := api.Inbox{Task: task.ID, Items: []api.InboxItem{}}
	for _, it := range items {
		item := api.InboxItem{InboxItem: it, ChildShort: board.Short(it.ChildID), News: news(it)}
		if child, err := s.st.Task(it.ChildID); err != nil {
			return nil, err
		} else if child != nil {
			item.ChildTitle = child.Title
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}
