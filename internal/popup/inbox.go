package popup

import (
	"fmt"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/tui"
)

// Inbox is the popup's view of the daemon's inbox: the items, which one is
// shown, and whether the panel is open. It turns them into the panel's
// State. Not safe for concurrent use: the agent serializes access.
type Inbox struct {
	Items   []core.InboxItem
	Current string // the shown item's session
	Open    bool
	Note    string // a line under the buttons ("Sending…", an error)
	Playing bool
	// Sessions names items (by session id) and gives their details line.
	Sessions map[string]*core.Session
	Home     string
}

// Set replaces the items (from a snapshot), keeping the shown one when it's
// still there; it returns the sessions whose items are new.
func (b *Inbox) Set(items []core.InboxItem, sessions map[string]*core.Session) []string {
	old := map[string]core.InboxItem{}
	for _, it := range b.Items {
		old[it.SessionID] = it
	}
	var fresh []string
	for _, it := range items {
		if o, ok := old[it.SessionID]; !ok || !o.At.Equal(it.At) || o.Text != it.Text {
			fresh = append(fresh, it.SessionID)
		}
	}
	b.Items, b.Sessions = items, sessions
	if b.index() < 0 {
		b.Current = ""
		if len(items) > 0 {
			b.Current = items[0].SessionID
		}
	}
	if len(items) == 0 {
		b.Open = false
	}
	return fresh
}

func (b *Inbox) index() int {
	for i, it := range b.Items {
		if it.SessionID == b.Current {
			return i
		}
	}
	return -1
}

// Item is the shown item (nil when there's none).
func (b *Inbox) Item() *core.InboxItem {
	if i := b.index(); i >= 0 {
		return &b.Items[i]
	}
	return nil
}

// Move shows the item delta places away (wrapping).
func (b *Inbox) Move(delta int) {
	n := len(b.Items)
	if n == 0 {
		return
	}
	i := b.index()
	if i < 0 {
		i = 0
	}
	b.Current = b.Items[((i+delta)%n+n)%n].SessionID
	b.Note = ""
}

// Show opens the panel on session id's item (or keeps the current one).
func (b *Inbox) Show(id string) {
	if id != "" {
		b.Current = id
	}
	if b.index() < 0 && len(b.Items) > 0 {
		b.Current = b.Items[0].SessionID
	}
	b.Open = len(b.Items) > 0
}

// Remove drops session id's item at once (the daemon follows), showing the
// next one; the panel closes when none are left.
func (b *Inbox) Remove(id string) {
	i := -1
	for j, it := range b.Items {
		if it.SessionID == id {
			i = j
		}
	}
	if i < 0 {
		return
	}
	b.Items = append(b.Items[:i:i], b.Items[i+1:]...)
	if b.Current == id {
		b.Current = ""
		if len(b.Items) > 0 {
			b.Current = b.Items[min(i, len(b.Items)-1)].SessionID
		}
	}
	if len(b.Items) == 0 {
		b.Open = false
	}
	b.Note = ""
}

// State is what the panel shows.
func (b *Inbox) State() State {
	it := b.Item()
	if !b.Open || it == nil {
		return State{}
	}
	s := State{Visible: true, Body: it.Text, Options: it.Options, Playing: b.Playing, Note: b.Note,
		Counter: fmt.Sprintf("%d of %d", b.index()+1, len(b.Items))}
	title, meta := "session", ""
	if sess := b.Sessions[it.SessionID]; sess != nil {
		title = sess.Name
		if title == "" && sess.BgID != "" {
			title = "session " + sess.BgID
		}
		meta = tui.StatusWord(sess.Status) + " · " + tui.Tilde(sess.Cwd, b.Home)
	}
	switch it.Kind {
	case core.InboxQuestion:
		meta = "asks you · " + meta
		s.Placeholder = "Pick an answer, or type your own and press Enter"
		s.CanReply = true
	case core.InboxPermission:
		meta = "wants permission · " + meta
	default:
		s.CanReply = true
		s.Placeholder = "Reply to " + title + " (Enter sends, Shift+Enter for a new line)"
	}
	s.Title, s.Meta = title, meta
	return s
}
