package sidebar

import (
	"context"
	"encoding/base64"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/brettmostert/agent-command/internal/ipc"
)

// Notifications (SPEC §8.5): the daemon names this view's sidebar as the
// emitter; the sidebar writes the terminal's notification escape sequence
// to its own tty, so macOS attributes it to the terminal and a click
// focuses this sidebar, which then shows the session (M0 N2, N6).

// clickWindow is how long after a notification focusing the sidebar
// counts as clicking it.
const clickWindow = 60 * time.Second

type emitMsg struct {
	seq       string
	sessionID string
}

// notices handles new notices from a snapshot.
func (m *Model) notices(snap ipc.Snapshot, first bool) tea.Cmd {
	var cmds []tea.Cmd
	for _, no := range snap.Notices {
		if no.Seq <= m.lastNotice {
			continue
		}
		m.lastNotice = no.Seq
		if first || no.Emitter != m.viewID {
			continue // old ones, or another sidebar's
		}
		no := no
		cmds = append(cmds, func() tea.Msg {
			if no.Kind == "withdraw" {
				if seq := Withdraw(m.act.Terminal(), no.SessionID); seq != "" {
					return emitMsg{seq: seq}
				}
				return nil
			}
			// Not when this view shows the session and is focused (§8.5).
			if m.view != nil && m.view.Selected == no.SessionID && m.act.ViewFocused(m.ctx) {
				return nil
			}
			return emitMsg{seq: Sequence(m.act.Terminal(), no), sessionID: no.SessionID}
		})
	}
	return tea.Batch(cmds...)
}

// Sequence is the notification escape sequence for terminal: Kitty OSC 99
// (base64 payloads, so names can't inject anything; urgent = critical,
// which shows a banner, M0 N1; same id replaces, N4; a=focus), Ghostty
// OSC 777.
func Sequence(terminal string, no ipc.Notice) string {
	if terminal == "kitty" {
		u := "1"
		if no.Urgent {
			u = "2"
		}
		b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
		id := safeID(no.SessionID)
		return "\x1b]99;i=" + id + ":d=0:e=1:u=" + u + ";" + b64(no.Title) + "\x1b\\" +
			"\x1b]99;i=" + id + ":d=1:e=1:p=body:a=focus;" + b64(no.Body) + "\x1b\\"
	}
	return "\x1b]777;notify;" + plainText(no.Title) + ";" + plainText(no.Body) + "\x07"
}

// Withdraw removes a notification (Kitty only, M0 N5).
func Withdraw(terminal, sessionID string) string {
	if terminal != "kitty" {
		return ""
	}
	return "\x1b]99;i=" + safeID(sessionID) + ":p=close;\x1b\\"
}

func safeID(id string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return -1
	}, id)
}

// plainText drops control characters and OSC 777's ';' separator.
func plainText(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r < 0x20, r == 0x7f, r >= 0x80 && r < 0xa0:
			return -1
		case r == ';':
			return ','
		}
		return r
	}, s)
}

// focused is the sidebar gaining focus: within clickWindow of a
// notification it was the click (N2/N6), so show that session.
func (m *Model) focused() tea.Cmd {
	if m.notified == "" || m.now().Sub(m.notifiedAt) > clickWindow {
		return nil
	}
	s := m.session(m.notified)
	m.notified = ""
	if s == nil {
		return nil
	}
	m.cursor = "s:" + s.ID
	return m.do(func(ctx context.Context) error { return m.act.Show(ctx, s) })
}
