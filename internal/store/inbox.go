package store

import "strings"

// Notified are the states a task's parent hears about: those where the task stopped and has
// something to say, or waits on someone.
var Notified = map[string]bool{"needs": true, "review": true, "done": true, "parked": true}

// ByTask is how history records a move a task made through hc, such as a coordinator closing its child.
func ByTask(id string) string { return "task:" + id }

// TaskOf is the task that made a move, or empty when it was you, a hook or the board.
func TaskOf(by string) string {
	id, found := strings.CutPrefix(by, "task:")
	if !found {
		return ""
	}
	return id
}

// InboxItem is one thing a parent task hears about one of its children: the state the child moved to.
type InboxItem struct {
	ID       int64   `json:"id"`
	ParentID string  `json:"parent_id"`
	ChildID  string  `json:"child_id"`
	State    string  `json:"state"`
	Ask      string  `json:"ask"`
	AskKind  string  `json:"ask_kind"`
	Last     string  `json:"last"`
	At       float64 `json:"at"`
	// DeliveredAt is when the board typed it into the parent's session. Zero while it waits.
	DeliveredAt float64 `json:"delivered_at"`
}

// inboxKept is how many delivered items the board keeps per parent, for hc inbox.
const inboxKept = 100

const inboxColumns = "id, parent_id, child_id, state, ask, ask_kind, last, at, delivered_at"

// notify files an item for a parent. A child has one item waiting at most: a newer state replaces it.
func (s *Store) notify(item InboxItem) error {
	if _, err := s.Exec("DELETE FROM inbox WHERE parent_id = ? AND child_id = ? AND delivered_at = 0",
		item.ParentID, item.ChildID); err != nil {
		return err
	}
	_, err := s.Exec("INSERT INTO inbox (parent_id, child_id, state, ask, ask_kind, last, at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		item.ParentID, item.ChildID, item.State, item.Ask, item.AskKind, item.Last, Now())
	return err
}

// Inbox runs a query that selects whole items. The clause follows "FROM inbox".
func (s *Store) Inbox(clause string, args ...any) ([]InboxItem, error) {
	rows, err := s.Query("SELECT "+inboxColumns+" FROM inbox "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InboxItem
	for rows.Next() {
		var it InboxItem
		if err := rows.Scan(&it.ID, &it.ParentID, &it.ChildID, &it.State, &it.Ask, &it.AskKind, &it.Last, &it.At,
			&it.DeliveredAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// Delivered marks items as typed into their parent's session, and trims the parent's oldest.
func (s *Store) Delivered(parentID string, ids []int64, at float64) error {
	for _, id := range ids {
		if _, err := s.Exec("UPDATE inbox SET delivered_at = ? WHERE id = ?", at, id); err != nil {
			return err
		}
	}
	_, err := s.Exec("DELETE FROM inbox WHERE parent_id = ? AND delivered_at > 0 AND id <= (SELECT id FROM inbox"+
		" WHERE parent_id = ? AND delivered_at > 0 ORDER BY id DESC LIMIT 1 OFFSET ?)", parentID, parentID, inboxKept)
	return err
}

// DropInbox removes an item that no longer says anything true.
func (s *Store) DropInbox(id int64) error {
	_, err := s.Exec("DELETE FROM inbox WHERE id = ?", id)
	return err
}

// LastDelivery is when the board last typed an inbox message into a parent's session. Zero: never.
func (s *Store) LastDelivery(parentID string) (float64, error) {
	var at float64
	err := s.QueryRow("SELECT coalesce(max(delivered_at), 0) FROM inbox WHERE parent_id = ?", parentID).Scan(&at)
	return at, err
}
