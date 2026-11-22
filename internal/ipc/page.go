package ipc

import (
	"encoding/base64"
	"fmt"
	"strconv"

	"github.com/brettmostert/agent-command/internal/core"
)

// Paging (design 8.5): a list op takes Page, and returns one page of items
// with NextCursor, which asks for the next; it's "" on the last page. A
// cursor is opaque: send back the one you were given.
const (
	PageSize = 100 // items in a page, when Limit is 0
	PageMax  = 500 // the most a Limit gets
)

// Page asks for one page.
type Page struct {
	Limit  int    `json:"limit,omitempty"`  // items in the page: 0 is PageSize, more than PageMax is PageMax
	Cursor string `json:"cursor,omitempty"` // "" for the first page
}

// Size is the page's size, from Limit.
func (p Page) Size() (int, error) {
	switch {
	case p.Limit < 0:
		return 0, fmt.Errorf("limit %d: a page has 0 items or more", p.Limit)
	case p.Limit == 0:
		return PageSize, nil
	case p.Limit > PageMax:
		return PageMax, nil
	}
	return p.Limit, nil
}

// A cursor is a position, in one of two forms: after an id (a list kept in
// the database, newest first, so items added meanwhile don't shift it), or
// an offset (a list read afresh each time).
const (
	cursorBefore = 'b'
	cursorOffset = 'o'
)

// BeforeCursor is the cursor for the items older than id.
func BeforeCursor(id int64) string { return encodeCursor(cursorBefore, id) }

// OffsetCursor is the cursor for the items from n on.
func OffsetCursor(n int) string { return encodeCursor(cursorOffset, int64(n)) }

func encodeCursor(kind byte, n int64) string {
	return base64.RawURLEncoding.EncodeToString(append([]byte{kind}, strconv.FormatInt(n, 10)...))
}

// Before is the id Cursor says to list from, older than it; 0 for the first
// page.
func (p Page) Before() (int64, error) { return p.decode(cursorBefore) }

// Offset is where in the list Cursor says to start.
func (p Page) Offset() (int, error) {
	n, err := p.decode(cursorOffset)
	return int(n), err
}

func (p Page) decode(kind byte) (int64, error) {
	if p.Cursor == "" {
		return 0, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(p.Cursor)
	if err != nil || len(b) < 2 || b[0] != kind {
		return 0, fmt.Errorf("cursor %q isn't one this list gave", p.Cursor)
	}
	n, err := strconv.ParseInt(string(b[1:]), 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("cursor %q isn't one this list gave", p.Cursor)
	}
	return n, nil
}

// InboxPage is a page of Inbox items, newest first.
type InboxPage struct {
	Items      []core.InboxItem `json:"items"`
	NextCursor string           `json:"next_cursor"`
}

// JobRunsRequest asks for a page of a job's runs.
type JobRunsRequest struct {
	ID string `json:"id"`
	Page
}

// JobRunPage is a page of a job's runs, newest first.
type JobRunPage struct {
	Items      []core.JobRun `json:"items"`
	NextCursor string        `json:"next_cursor"`
}

// ConversationPage is a page of earlier conversations, the most recently
// active first.
type ConversationPage struct {
	Items      []Conversation `json:"items"`
	NextCursor string         `json:"next_cursor"`
}

// PageOf is the page of list, already in order, from p's offset.
func PageOf[T any](list []T, p Page) ([]T, string, error) {
	size, err := p.Size()
	if err != nil {
		return nil, "", err
	}
	from, err := p.Offset()
	if err != nil {
		return nil, "", err
	}
	if from >= len(list) {
		return []T{}, "", nil
	}
	to := min(from+size, len(list))
	next := ""
	if to < len(list) {
		next = OffsetCursor(to)
	}
	return list[from:to], next, nil
}
