package client

import (
	"cmp"
	"context"
	"slices"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/remote/api"
)

// applyEvent merges an event from a host into what's here: its added
// sessions' state, their inbox items and notifications. The daemon is told
// after c.mu is released.
func (c *Client) applyEvent(h *hostConn, ev api.Event) {
	var ops []func(context.Context) error
	c.mu.Lock()
	host := h.cfg.Name
	byRemote := map[string]*added{}
	for _, a := range c.sortedAddedLocked(host) {
		byRemote[a.RemoteID] = a
	}
	update := func(a *added, s api.Session) {
		if a.Archived {
			return
		}
		u := Update{Session: s}
		if a.hostUnseen != nil && *a.hostUnseen != s.Unseen {
			u.Unseen = &s.Unseen
		}
		a.hostUnseen = &s.Unseen
		ops = append(ops, func(ctx context.Context) error { return c.b.Update(ctx, a.ID, u) })
	}
	gone := func(a *added) {
		c.inbox.set(a.ID, host, nil)
		ops = append(ops, func(ctx context.Context) error { return c.b.Gone(ctx, a.ID) })
	}
	switch ev.Type {
	case api.EventSnapshot:
		live := map[string]api.Session{}
		for _, s := range ev.Sessions {
			live[s.ID] = s
		}
		items := map[string][]api.InboxItem{}
		for _, it := range ev.Inbox {
			items[it.SessionID] = append(items[it.SessionID], it)
		}
		h.items = items
		for _, a := range byRemote {
			if s, ok := live[a.RemoteID]; ok {
				update(a, s)
				c.inbox.set(a.ID, host, items[a.RemoteID])
			} else {
				gone(a)
			}
		}
	case api.EventAdded, api.EventChanged:
		if a := byRemote[sessionID(ev)]; a != nil {
			update(a, *ev.Session)
		}
	case api.EventRemoved:
		delete(h.items, ev.ID)
		if a := byRemote[ev.ID]; a != nil {
			gone(a)
		}
	case api.EventInbox:
		h.items[ev.ID] = ev.Inbox
		if a := byRemote[ev.ID]; a != nil {
			c.inbox.set(a.ID, host, ev.Inbox)
		}
	case api.EventNotice:
		if ev.Notice != nil {
			if a := byRemote[ev.Notice.SessionID]; a != nil && !a.Archived {
				n := *ev.Notice
				ops = append(ops, func(context.Context) error { c.b.Notice(a.ID, host, n); return nil })
			}
		}
	}
	h.notifyLocked()
	c.mu.Unlock()
	for _, op := range ops {
		if err := op(c.ctx); err != nil {
			c.o.Log.Printf("remote host %s: %v", host, err)
		}
	}
}

func sessionID(ev api.Event) string {
	if ev.Session == nil {
		return ""
	}
	return ev.Session.ID
}

// inbox is the open inbox items of the added sessions, as their hosts last
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

// set replaces a session's items with what its host says.
func (in *inbox) set(session, host string, items []api.InboxItem) {
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
		out = append(out, core.InboxItem{ID: id, SessionID: session, Kind: it.Kind, Text: it.Text, Options: it.Options,
			Multipart: it.Multipart, At: it.At, Read: it.Read, State: core.InboxOpen, Host: host})
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

// Inbox is the open items of the added sessions, oldest first.
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

// SessionInbox is one added session's open items, newest first.
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

// InboxItem is an added session's open item, by its local id.
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
