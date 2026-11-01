package popup

import "github.com/brettmostert/agent-command/internal/core"

// Latest remembers, per session, the last thing worth reading aloud, even
// after the inbox dropped it (the user was looking at that session, so no
// popup was needed). Not safe for concurrent use: the agent serializes it.
type Latest struct {
	done    map[string]core.InboxItem // the last finished turn
	pending map[string]core.InboxItem // the last question, permission or error
}

// Observe records the items of a snapshot's inbox.
func (l *Latest) Observe(items []core.InboxItem) {
	if l.done == nil {
		l.done, l.pending = map[string]core.InboxItem{}, map[string]core.InboxItem{}
	}
	for _, it := range items {
		if it.Kind == core.InboxDone {
			l.done[it.SessionID] = it
		} else {
			l.pending[it.SessionID] = it
		}
	}
}

// Text is what to read aloud for the session: the latest of its pending
// question or permission (only while it is still waiting on it) and its
// last finished turn. ok is false when there is nothing.
func (l *Latest) Text(id string, s *core.Session) (text string, ok bool) {
	d, haveDone := l.done[id]
	p, havePending := l.pending[id]
	if havePending && p.Kind != core.InboxError && (s == nil || s.Status != core.StatusWaiting) {
		havePending = false // already answered
	}
	switch {
	case havePending && (!haveDone || p.At.After(d.At)):
		return p.Text, true
	case haveDone:
		return d.Text, true
	}
	return "", false
}
