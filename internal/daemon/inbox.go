package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/store"
	"github.com/brettmostert/agent-command/internal/term"
)

// Handled items are kept this long.
const inboxKeep = 30 * 24 * time.Hour

// pasteEnterDelay separates pasted text from its Enter: in one burst, Claude reads
// the Enter as a new line.
const pasteEnterDelay = 300 * time.Millisecond

// applyInbox updates the inbox for event e of session s (d.mu is held).
// applyInbox opens or closes s's Inbox item for e. st is the store to write to:
// the transaction its caller applies the event in.
func applyInbox(ctx context.Context, st *store.Store, s *core.Session, e core.Event) error {
	item, drop := core.InboxEffect(s, e)
	switch {
	case item != nil:
		return st.AddInboxItem(ctx, *item)
	case drop:
		return st.CloseInboxItems(ctx, s.ID)
	}
	return nil
}

// tidyInbox closes items nothing can answer any more, and deletes old handled ones.
// It reports whether the queue changed.
func (d *Daemon) tidyInbox(ctx context.Context) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	changed, err := d.store.CloseUnanswerableItems(ctx)
	if err != nil {
		return false, err
	}
	return changed, d.store.PruneInbox(ctx, d.cfg.Now().Add(-inboxKeep))
}

// inboxHistory is a page of the Inbox: one session's, or everyone's.
func (d *Daemon) inboxHistory(ctx context.Context, r ipc.InboxRequest) (ipc.InboxPage, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	items, next, err := pageBefore(r.Page, func(before int64, n int) ([]core.InboxItem, error) {
		return d.store.InboxBefore(ctx, r.SessionID, before, n)
	}, func(it core.InboxItem) int64 { return it.ID })
	return ipc.InboxPage{Items: items, NextCursor: next}, err
}

// pageBefore is a page of a list the store keeps newest first, by id: fetch
// gets n items older than before. One more than the page says whether
// there's another.
func pageBefore[T any](p ipc.Page, fetch func(before int64, n int) ([]T, error), idOf func(T) int64) ([]T, string, error) {
	size, err := p.Size()
	if err != nil {
		return nil, "", badRequest("%v", err)
	}
	before, err := p.Before()
	if err != nil {
		return nil, "", badRequest("%v", err)
	}
	items, err := fetch(before, size+1)
	if err != nil {
		return nil, "", err
	}
	if items == nil {
		items = []T{}
	}
	if len(items) <= size {
		return items, "", nil
	}
	items = items[:size]
	return items, ipc.BeforeCursor(idOf(items[size-1])), nil
}

// answerInbox types an answer into the item's session. It checks the session is
// waiting for one first, because text sent at the wrong moment lands somewhere you
// didn't choose: in a prompt you haven't seen, or in a plain shell.
func (d *Daemon) answerInbox(ctx context.Context, a ipc.InboxAnswer) error {
	if a.Host != "" { // a host's item
		return d.answerOnHost(ctx, a)
	}
	d.mu.Lock()
	var keys [][]byte
	it, err := d.store.InboxItem(ctx, a.ID)
	if err == nil {
		var s *core.Session
		if s, err = d.store.Session(ctx, it.SessionID); err == nil {
			keys, err = answerKeys(it, s, a)
		}
	}
	d.mu.Unlock()
	if err != nil {
		return err
	}
	if err := d.mayAnswer(ctx, it.Kind); err != nil {
		return err
	}
	if err := d.typeKeys(it.SessionID, keys); err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	err = d.store.HandleInboxItem(ctx, a.ID, core.InboxResponded)
	if errors.Is(err, store.ErrNotFound) {
		return nil // the session already moved on, because of this answer
	}
	return err
}

// typeKeys types an answer into a session, without d.mu held: it waits
// between the text and its Enter.
func (d *Daemon) typeKeys(sessionID string, keys [][]byte) error {
	for i, k := range keys {
		if i > 0 {
			time.Sleep(pasteEnterDelay)
		}
		if err := d.sendToHost(sessionID, k); err != nil {
			return fmt.Errorf("couldn't reach the session: %w", err)
		}
	}
	return nil
}

// answerKeys is what to type for an answer to item it of session s, or why
// it can't be sent.
func answerKeys(it *core.InboxItem, s *core.Session, a ipc.InboxAnswer) ([][]byte, error) {
	if it.State != core.InboxOpen {
		return nil, errors.New("this is no longer waiting for an answer")
	}
	// An answer is typed into the session's pane. For Claude, a conversation id
	// says Claude runs there. agy's comes only with its first hook, after its
	// sign-in and folder trust prompts, which it reports anyway: its host running
	// is enough. A session on a host says through its status.
	if s.Stopped() || (s.ClaudeSessionID == "" && !s.IsAntigravity() && !s.OnHost()) {
		return nil, fmt.Errorf("no %s is running in this session", core.AgentName(s.Harness))
	}
	switch it.Kind {
	case core.InboxQuestion, core.InboxPermission:
		if it.Multipart {
			return nil, errors.New("this question has several parts: answer it in the session")
		}
		if s.Status != core.StatusWaiting {
			return nil, errors.New("this session isn't waiting for an answer")
		}
		if a.Option < 1 || a.Option > len(it.Options) || a.Option > 9 {
			return nil, errors.New("that isn't one of the options")
		}
		// The option's number picks it, in Claude's prompts and agy's alike, with
		// no Enter.
		return [][]byte{{byte('0' + a.Option)}}, nil
	default:
		if s.Status != core.StatusDone && s.Status != core.StatusError {
			return nil, errors.New("the session is busy: go to it to reply")
		}
		text := strings.ReplaceAll(a.Text, "\x1b", "")
		if strings.TrimSpace(text) == "" {
			return nil, errors.New("the reply is empty")
		}
		return [][]byte{[]byte("\x1b[200~" + text + "\x1b[201~"), []byte("\r")}, nil
	}
}

func (d *Daemon) sendToHost(sessionID string, data []byte) error {
	if d.cfg.SendToHost != nil {
		return d.cfg.SendToHost(sessionID, data)
	}
	return term.Send(d.cfg.Paths.HostSocket(sessionID), data)
}
