package client

import (
	"cmp"
	"slices"

	"github.com/brettmostert/agent-command/internal/core"
)

// inbox is the open inbox items of the hosts' sessions, as their hosts last
// said, with local ids: negative, so they never meet the daemon's own.
type inbox struct {
	ids     map[inboxKey]int64
	next    int64
	items   map[string][]core.InboxItem // by local session id
	read    map[int64]bool              // read here
	handled map[int64]bool              // dismissed or answered here
}

type inboxKey struct {
	host string
	id   int64
}

func newInbox() inbox {
	return inbox{ids: map[inboxKey]int64{}, items: map[string][]core.InboxItem{}, read: map[int64]bool{}, handled: map[int64]bool{}}
}

// setItems replaces a session's items with what its host says: each kept
// under an id of its own here (negative, so it never meets a local one).
func (in *inbox) setItems(session, host string, items []core.InboxItem) {
	keep := map[int64]bool{}
	var out []core.InboxItem
	for _, it := range items {
		k := inboxKey{host, it.ID}
		id, ok := in.ids[k]
		if !ok {
			in.next--
			id = in.next
			in.ids[k] = id
		}
		keep[id] = true
		it.ID, it.SessionID, it.State, it.Host = id, session, core.InboxOpen, host
		out = append(out, it)
	}
	// What was read or handled here stays so while the host still has it.
	for _, old := range in.items[session] {
		if !keep[old.ID] {
			delete(in.read, old.ID)
			delete(in.handled, old.ID)
			for k, id := range in.ids {
				if id == old.ID {
					delete(in.ids, k)
				}
			}
		}
	}
	if len(out) == 0 {
		delete(in.items, session)
	} else {
		in.items[session] = out
	}
}

// set clears a session's items, or replaces them.
func (in *inbox) set(session, host string, items []core.InboxItem) { in.setItems(session, host, items) }

// Inbox is the open items of the hosts' sessions, oldest first.
func (c *Client) Inbox() []core.InboxItem {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []core.InboxItem
	for _, items := range c.inbox.items {
		for _, it := range items {
			if !c.inbox.handled[it.ID] {
				it.Read = it.Read || c.inbox.read[it.ID]
				out = append(out, it)
			}
		}
	}
	slices.SortFunc(out, func(a, b core.InboxItem) int { return cmp.Or(a.At.Compare(b.At), cmp.Compare(b.ID, a.ID)) })
	return out
}

// SessionInbox is one host session's open items, newest first.
func (c *Client) SessionInbox(session string) []core.InboxItem {
	var out []core.InboxItem
	for _, it := range c.Inbox() {
		if it.SessionID == session {
			out = append(out, it)
		}
	}
	slices.Reverse(out)
	return out
}

// InboxItem is a host session's open item, by its id here.
func (c *Client) InboxItem(id int64) (core.InboxItem, bool) {
	for _, it := range c.Inbox() {
		if it.ID == id {
			return it, true
		}
	}
	return core.InboxItem{}, false
}

// SetInboxRead marks items read or unread here.
func (c *Client) SetInboxRead(ids []int64, read bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range ids {
		c.inbox.read[id] = read
	}
	c.changedLocked()
}

// HandleInbox takes items out of the queue here: dismissed, or answered.
// The host's own inbox follows when its session moves on.
func (c *Client) HandleInbox(ids []int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range ids {
		c.inbox.handled[id] = true
	}
	c.changedLocked()
}
