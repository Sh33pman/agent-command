package client

import (
	"encoding/json"
	"slices"
	"strconv"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// Each host's tree: its sessions, groups and sets as the host has them, its
// open inbox items and its notices, kept from its event stream (subscribe).
// Ids stay as the host made them. Each of its own sessions that isn't
// archived gets a local socket that relays to it (proxy.go).

// treeTypes are what the client follows of a host.
var treeTypes = []string{ipc.EntitySession, ipc.EntityGroup, ipc.EntitySet, ipc.EntityInbox, ipc.EntityNotice}

type tree struct {
	sessions map[string]*ipc.Session
	groups   map[string]*ipc.Group
	sets     map[string]ipc.Set
	inbox    map[int64]core.InboxItem
	order    map[string][]string // ids by entity type, as they came
}

func newTree() *tree {
	return &tree{sessions: map[string]*ipc.Session{}, groups: map[string]*ipc.Group{}, sets: map[string]ipc.Set{},
		inbox: map[int64]core.InboxItem{}, order: map[string][]string{}}
}

func (t *tree) keep(typ, id string) {
	if !slices.Contains(t.order[typ], id) {
		t.order[typ] = append(t.order[typ], id)
	}
}

func (t *tree) drop(typ, id string) {
	t.order[typ] = slices.DeleteFunc(t.order[typ], func(x string) bool { return x == id })
}

// apply follows one event, and returns a notice the host decided on now. Only
// the host's own entities are kept: one it shows from yet another host has
// that host's ids, which can be the same as its own (every Default is
// "default"), and is that host's to offer.
func (t *tree) apply(ev ipc.Event) *ipc.Notice {
	if ev.Type == ipc.EventSnapshot && ev.Snapshot != nil {
		*t = *newTree()
		s := ev.Snapshot
		for _, x := range s.Sessions {
			if own(x) {
				t.sessions[x.ID] = x
				t.keep(ipc.EntitySession, x.ID)
			}
		}
		for _, x := range s.Groups {
			if x.Host == "" {
				t.groups[x.ID] = x
				t.keep(ipc.EntityGroup, x.ID)
			}
		}
		for _, x := range s.Sets {
			if x.Host == "" {
				t.sets[x.ID] = x
				t.keep(ipc.EntitySet, x.ID)
			}
		}
		for _, x := range s.Inbox {
			if x.Host == "" {
				t.inbox[x.ID] = x
			}
		}
		return nil
	}
	if ev.Host != "" && ev.Entity != ipc.EntityNotice {
		return nil // yet another host's
	}
	removed := ev.Type == ipc.EventRemoved
	switch ev.Entity {
	case ipc.EntitySession:
		var x ipc.Session
		if removed || json.Unmarshal(ev.Data, &x) != nil {
			delete(t.sessions, ev.ID)
			t.drop(ev.Entity, ev.ID)
			return nil
		}
		t.sessions[ev.ID] = &x
		t.keep(ev.Entity, ev.ID)
	case ipc.EntityGroup:
		var x ipc.Group
		if removed || json.Unmarshal(ev.Data, &x) != nil {
			delete(t.groups, ev.ID)
			t.drop(ev.Entity, ev.ID)
			return nil
		}
		t.groups[ev.ID] = &x
		t.keep(ev.Entity, ev.ID)
	case ipc.EntitySet:
		var x ipc.Set
		if removed || json.Unmarshal(ev.Data, &x) != nil {
			delete(t.sets, ev.ID)
			t.drop(ev.Entity, ev.ID)
			return nil
		}
		t.sets[ev.ID] = x
		t.keep(ev.Entity, ev.ID)
	case ipc.EntityInbox:
		id, _ := strconv.ParseInt(ev.ID, 10, 64)
		var x core.InboxItem
		if removed || json.Unmarshal(ev.Data, &x) != nil {
			delete(t.inbox, id)
			return nil
		}
		t.inbox[id] = x
	case ipc.EntityNotice:
		var n ipc.Notice
		if ev.Type == ipc.EventAdded && json.Unmarshal(ev.Data, &n) == nil {
			return &n
		}
	}
	return nil
}

// own says whether a host's session is its own, not one it uses from yet
// another host: that host's to offer.
func own(s *ipc.Session) bool { return s.Host == "" }

// syncLocked follows a host's tree here: a socket for each of its own
// sessions that isn't archived (c.mu is held). Their Inbox items are read
// from the tree as they are (Inbox).
func (c *Client) syncLocked(h *hostConn) {
	host := h.cfg.Name
	live := map[string]bool{}
	for _, s := range h.tree.sessions {
		if own(s) && s.Status != core.StatusArchived {
			live[s.ID] = true
		}
	}
	for id, a := range c.added {
		if a.Host == host && !live[id] {
			c.closeProxyLocked(a)
			delete(c.added, id)
		}
	}
	for id := range live {
		a := c.added[id]
		if a == nil {
			a = &added{Added: Added{ID: id, Host: host, RemoteID: id}}
			c.added[id] = a
		}
		c.openLocked(a)
	}
}

// Tree is the hosts' sessions, groups and sets, each with its host, as the
// hosts have them.
type Tree struct {
	Sessions []*ipc.Session
	Groups   []*ipc.Group
	Sets     []ipc.Set
}

// Tree is what the known hosts have, as they last said: each host's own
// sessions, its groups and its sets, in the order they came, named by host.
func (c *Client) Tree() Tree {
	var out Tree
	if c == nil {
		return out
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, name := range c.order {
		h := c.hosts[name]
		if h == nil || h.tree == nil {
			continue
		}
		t := h.tree
		for _, id := range t.order[ipc.EntitySession] {
			if s := t.sessions[id]; s != nil && own(s) {
				x := *s
				x.Host, x.RemoteID, x.HostGone = name, id, false
				// What can be done with it from here: what its host says, but
				// its folder isn't on this machine, and a job here can't go on
				// in it.
				x.Capabilities = slices.DeleteFunc(slices.Clone(s.Capabilities), func(c string) bool {
					return c == core.CanOpenInEditor || c == core.CanContinueInJob
				})
				out.Sessions = append(out.Sessions, &x)
			}
		}
		for _, id := range t.order[ipc.EntityGroup] {
			if g := t.groups[id]; g != nil {
				x := *g
				x.Host = name
				out.Groups = append(out.Groups, &x)
			}
		}
		for _, id := range t.order[ipc.EntitySet] {
			if st, ok := t.sets[id]; ok && st.Host == "" {
				st.Host = name
				out.Sets = append(out.Sets, st)
			}
		}
	}
	return out
}

// HostOf is the known host a session, group or set is on, by its id.
func (c *Client) HostOf(id string) (string, bool) {
	if c == nil || id == "" {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for name, h := range c.hosts {
		if h.tree == nil {
			continue
		}
		if s := h.tree.sessions[id]; s != nil && own(s) {
			return name, true
		}
		if h.tree.groups[id] != nil {
			return name, true
		}
		if st, ok := h.tree.sets[id]; ok && st.Host == "" {
			return name, true
		}
	}
	return "", false
}
