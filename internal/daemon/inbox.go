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

// inboxHistoryLimit caps one read of the history.
const inboxHistoryLimit = 500

// pasteEnterDelay separates pasted text from its Enter: in one burst, Claude reads
// the Enter as a new line.
const pasteEnterDelay = 300 * time.Millisecond

// applyInbox updates the inbox for event e of session s (d.mu is held).
func (d *Daemon) applyInbox(ctx context.Context, s *core.Session, e core.Event) error {
	item, drop := core.InboxEffect(s, e)
	switch {
	case item != nil:
		return d.store.AddInboxItem(ctx, *item)
	case drop:
		return d.store.CloseInboxItems(ctx, s.ID)
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

func (d *Daemon) inboxHistory(ctx context.Context, sessionID string) ([]core.InboxItem, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.store.InboxHistory(ctx, sessionID, inboxHistoryLimit)
}

// answerInbox types an answer into the item's session. It checks the session is
// waiting for one first, because text sent at the wrong moment lands somewhere you
// didn't choose: in a prompt you haven't seen, or in a plain shell.
func (d *Daemon) answerInbox(ctx context.Context, a ipc.InboxAnswer) error {
	d.mu.Lock()
	keys, err := d.answerKeys(ctx, a)
	var sessionID string
	if err == nil {
		var it *core.InboxItem
		if it, err = d.store.InboxItem(ctx, a.ID); err == nil {
			sessionID = it.SessionID
		}
	}
	d.mu.Unlock()
	if err != nil {
		return err
	}

	// Not while holding d.mu: this waits between the text and its Enter.
	for i, k := range keys {
		if i > 0 {
			time.Sleep(pasteEnterDelay)
		}
		if err := d.sendToHost(sessionID, k); err != nil {
			return fmt.Errorf("couldn't reach the session: %w", err)
		}
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	err = d.store.HandleInboxItem(ctx, a.ID, core.InboxResponded)
	if errors.Is(err, store.ErrNotFound) {
		return nil // the session already moved on, because of this answer
	}
	return err
}

// answerKeys is what to type for an answer, or why it can't be sent (d.mu is held).
func (d *Daemon) answerKeys(ctx context.Context, a ipc.InboxAnswer) ([][]byte, error) {
	it, err := d.store.InboxItem(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	if it.State != core.InboxOpen {
		return nil, errors.New("this is no longer waiting for an answer")
	}
	s, err := d.store.Session(ctx, it.SessionID)
	if err != nil {
		return nil, err
	}
	if s.ClaudeSessionID == "" || s.Stopped() {
		return nil, errors.New("no Claude is running in this session")
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
