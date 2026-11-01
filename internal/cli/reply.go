package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/term"
)

// Answering from the reply popup (SPEC §8.8): acmd hosts the session, so it
// types the answer straight into the session's PTY (no hidden attach).
// Checked 2026-11-01 against Claude Code 2.1.316: a question's option is
// picked by its number key; text is pasted (bracketed paste keeps its
// lines) and sent with Enter.

// replyInput is what acmd types for an answer: text (pasted, then Enter)
// or an option (its number key, 0-based).
type replyInput struct {
	Text   string
	Option int // -1: none
}

func (r replyInput) keys() []byte {
	if r.Option >= 0 {
		return []byte(fmt.Sprint(r.Option + 1))
	}
	return []byte("\x1b[200~" + r.Text + "\x1b[201~")
}

// Timings (tests shorten them): the pause before Enter after a paste, and
// how long to wait for the session to move on.
var (
	replyEnterDelay = 300 * time.Millisecond
	replyTake       = 10 * time.Second
)

// deliverReply answers session s with in.
func deliverReply(ctx context.Context, call caller, s *core.Session, in replyInput) error {
	if s.Kind == core.KindExternal {
		if s.External == nil {
			return errors.New("don't know which pane that session is in: answer it there")
		}
		if in.Option >= 0 {
			if s.External.Terminal != "kitty" {
				return errors.New("answer it in its own pane")
			}
			return kittyAt(s.External.KittySocket).SendText(ctx, s.External.KittyWindow, string(in.keys()))
		}
		return typeInto(ctx, *s.External, in.Text)
	}
	if s.Stopped() || s.NeedsResume() {
		var r core.Session
		if err := call(ipc.OpResume, ipc.Mutation{ID: s.ID}, &r); err != nil {
			return err
		}
		s = &r
	}
	p, err := paths.Default()
	if err != nil {
		return err
	}
	from := s.Status
	return typeViaHost(ctx, p.HostSocket(s.ID), in.keys(), in.Option < 0, func() bool {
		var snap ipc.Snapshot
		if call(ipc.OpSnapshot, nil, &snap) != nil {
			return false
		}
		for _, x := range snap.Sessions {
			if x.ID == s.ID {
				return x.Status != from || x.LastEventAt.After(s.LastEventAt)
			}
		}
		return false
	})
}

// typeViaHost writes keys (then Enter when enter) straight into the
// session's PTY through its host, and waits until moved reports the
// session reacted. Nothing attaches, so no view notices.
func typeViaHost(ctx context.Context, socket string, keys []byte, enter bool, moved func() bool) error {
	if err := term.Send(socket, keys); err != nil {
		return errors.New("the session's host isn't running: show the session to resume it")
	}
	if enter {
		select { // let it take the paste
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(replyEnterDelay):
		}
		if err := term.Send(socket, []byte("\r")); err != nil {
			return err
		}
	}
	for deadline := time.Now().Add(replyTake); time.Now().Before(deadline); {
		if moved() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return errors.New("Claude didn't take the answer yet (it may be busy: it queues it); check the session") //nolint:staticcheck // ST1005: product name
}

// optionIndex is the 0-based option of an item's answer label.
func optionIndex(it *core.InboxItem, n int) (replyInput, error) {
	if n < 0 || n >= len(it.Options) {
		return replyInput{}, fmt.Errorf("no option %d", n+1)
	}
	return replyInput{Option: n}, nil
}

// replyText is a free-text answer.
func replyText(text string) replyInput {
	return replyInput{Text: strings.TrimSpace(text), Option: -1}
}
