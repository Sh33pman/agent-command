package client

import (
	"cmp"
	"slices"

	"github.com/brettmostert/agent-command/internal/core"
)

// The hosts' Inbox items are read from their trees as the hosts have them,
// each with its host's own id and its host (design 7.4): nothing here
// renumbers them. Reading, dismissing and answering one is done on its host,
// so every client of the host agrees.

// Inbox is the open items of the hosts' own sessions, oldest first.
func (c *Client) Inbox() []core.InboxItem {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []core.InboxItem
	for name, h := range c.hosts {
		if h.tree == nil {
			continue
		}
		for _, it := range h.tree.inbox {
			s := h.tree.sessions[it.SessionID]
			if s == nil || !own(s) || s.Status == core.StatusArchived || it.State != core.InboxOpen {
				continue
			}
			it.Host = name
			out = append(out, it)
		}
	}
	slices.SortFunc(out, func(a, b core.InboxItem) int {
		return cmp.Or(a.At.Compare(b.At), cmp.Compare(a.Host, b.Host), cmp.Compare(a.ID, b.ID))
	})
	return out
}

// SessionInbox is one of host's sessions' open items, newest first.
func (c *Client) SessionInbox(host, session string) []core.InboxItem {
	var out []core.InboxItem
	for _, it := range c.Inbox() {
		if it.Host == host && it.SessionID == session {
			out = append(out, it)
		}
	}
	slices.Reverse(out)
	return out
}

// InboxItem is one of host's open items, by its id there.
func (c *Client) InboxItem(host string, id int64) (core.InboxItem, bool) {
	for _, it := range c.Inbox() {
		if it.Host == host && it.ID == id {
			return it, true
		}
	}
	return core.InboxItem{}, false
}
