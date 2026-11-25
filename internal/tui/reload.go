package tui

import (
	"encoding/json"
	"os"
	"slices"
	"strings"

	"github.com/rouxlabs/headcoach/internal/format"
)

// stateEnv carries the view of the board to the binary it reloads into, so that a reload after an
// update looks like nothing happened.
const stateEnv = "HC_BOARD_STATE"

// boardState is what a reload keeps of the board: what it shows, and where the cursors are.
type boardState struct {
	ShowDone  bool   `json:"show_done,omitempty"`
	Order     string `json:"order,omitempty"`
	Filter    string `json:"filter,omitempty"`
	View      string `json:"view,omitempty"`
	Cursor    string `json:"cursor,omitempty"` // the key of the row under the cursor
	Row       int    `json:"row,omitempty"`    // its position, for when that row is gone
	Top       int    `json:"top,omitempty"`    // the first row on screen
	Detail    string `json:"detail,omitempty"` // the task the detail view shows
	DetailTop int    `json:"detail_top,omitempty"`
	Schedule  int    `json:"schedule,omitempty"`
	Review    int    `json:"review,omitempty"`
	Column    int    `json:"column,omitempty"`
}

// reloadViews are the views a reload keeps, by name: those the board reloads in, as idle says.
var reloadViews = map[string]view{
	"board": viewBoard, "detail": viewDetail, "schedules": viewSchedules, "review": viewReview, "columns": viewColumns,
}

// saveState writes the view of the board for the binary it reloads into.
func (m model) saveState() string {
	s := boardState{ShowDone: m.showDone, Order: m.order, Filter: m.needle, Top: m.top,
		Row: m.cursor, Schedule: m.schedCursor, Review: m.reviewCursor, Column: m.colCursor}
	for name, v := range reloadViews {
		if v == m.view {
			s.View = name
		}
	}
	if m.cursor < len(m.keys) {
		s.Cursor = m.keys[m.cursor]
	}
	if m.view == viewDetail {
		s.Detail, s.DetailTop = m.detailID, m.detail.YOffset
	}
	out, _ := json.Marshal(s)
	return string(out)
}

// restoreState puts back the view a reload saved. The rows come with the first poll: the cursor
// and the scroll wait for it. A value it cannot read leaves the board as a fresh one.
func (m *model) restoreState(saved string) {
	var s boardState
	if saved == "" || json.Unmarshal([]byte(saved), &s) != nil {
		return
	}
	m.showDone, m.needle = s.ShowDone, s.Filter
	if slices.Contains(format.Sorts, s.Order) {
		m.order = s.Order
	}
	if v, ok := reloadViews[s.View]; ok && (v != viewDetail || s.Detail != "") {
		m.view = v
	}
	m.detailID = s.Detail
	m.jump, m.restoreRow, m.restoreTop, m.restoreDetail = s.Cursor, max(0, s.Row), max(0, s.Top), max(0, s.DetailTop)
	m.schedCursor, m.reviewCursor = max(0, s.Schedule), max(0, s.Review)
	m.colCursor = max(0, min(s.Column, len(m.layout)-1))
	m.reviewLoading = m.view == viewReview
}

// reloadEnv is the environment of the binary the board reloads into: this one, with the view of
// the board in place of any older one.
func (m model) reloadEnv() []string {
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, stateEnv+"=") })
	return append(env, stateEnv+"="+m.saveState())
}
