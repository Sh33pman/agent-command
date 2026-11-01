package sidebar

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

func TestSequence(t *testing.T) {
	evil := "name\x1b]0;pwned\x07; $(rm -rf ~)"
	no := ipc.Notice{SessionID: "01ABC;x", Title: "⚠ " + evil, Body: "body", Urgent: true}
	k := Sequence("kitty", no)
	if !strings.HasPrefix(k, "\x1b]99;i=01ABCx:d=0:e=1:u=2;"+base64.StdEncoding.EncodeToString([]byte(no.Title))+"\x1b\\") ||
		!strings.HasSuffix(k, ":d=1:e=1:p=body:a=focus;"+base64.StdEncoding.EncodeToString([]byte("body"))+"\x1b\\") {
		t.Errorf("kitty: %q", k)
	}
	if strings.Count(k, "\x1b") != 4 {
		t.Errorf("injected escapes in kitty: %q", k)
	}
	no.Urgent = false
	if !strings.Contains(Sequence("kitty", no), ":u=1;") {
		t.Error("normal urgency")
	}
	g := Sequence("ghostty", no)
	if g != "\x1b]777;notify;⚠ name]0,pwned, $(rm -rf ~);body\x07" {
		t.Errorf("ghostty: %q", g)
	}
	if Withdraw("kitty", "a b") != "\x1b]99;i=ab:p=close;\x1b\\" || Withdraw("ghostty", "x") != "" {
		t.Error("withdraw")
	}
}

func TestNotices(t *testing.T) {
	snap := listSnap()
	h := newHarness(t, snap, "a")
	notice := func(seq uint64, sid, kind, emitter string) ipc.Notice {
		return ipc.Notice{Seq: seq, SessionID: sid, Kind: kind, Title: "T" + sid, Body: "B", Emitter: emitter}
	}
	// Notices from before the sidebar started are never shown.
	snap.Notices = []ipc.Notice{notice(1, "b", "waiting", "v1")}
	h2 := newHarness(t, snap, "a")
	if h2.m.lastNotice != 1 || h2.m.notified != "" {
		t.Errorf("old notice shown: %+v", h2.m)
	}

	var emitted []string
	run := func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		switch msg := cmd().(type) {
		case tea.BatchMsg:
			for _, c := range msg {
				if c != nil {
					if m, ok := c().(emitMsg); ok {
						emitted = append(emitted, m.seq)
						h.m.Update(m)
					}
				}
			}
		case emitMsg:
			emitted = append(emitted, msg.seq)
			h.m.Update(msg)
		}
	}
	snap.Notices = []ipc.Notice{
		notice(2, "b", "waiting", "v1"), // emitted
		notice(3, "c", "done", "v2"),    // another sidebar's
		notice(4, "a", "done", "v1"),    // shown here, but the view isn't focused: emitted
		notice(5, "b", "withdraw", "v1"),
	}
	run(h.m.notices(snap, false))
	if len(emitted) != 3 || !strings.Contains(emitted[0], "i=b:") || !strings.Contains(emitted[1], "i=a:") || !strings.Contains(emitted[2], "p=close") {
		t.Errorf("emitted %q", emitted)
	}
	if h.m.notified != "a" {
		t.Errorf("notified %q", h.m.notified)
	}
	// The view shows a and is focused: no notification for a.
	h.f.focused = true
	emitted = nil
	snap.Notices = append(snap.Notices, notice(6, "a", "done", "v1"))
	run(h.m.notices(snap, false))
	if len(emitted) != 0 {
		t.Errorf("suppressed notice emitted: %q", emitted)
	}

	// Focus within the click window shows the notified session, once.
	h.m.notified, h.m.notifiedAt = "b", t0
	h.send(tea.FocusMsg{})
	if last := h.f.shown[len(h.f.shown)-1]; last != "b" || h.m.notified != "" {
		t.Errorf("click: shown %v", h.f.shown)
	}
	n := len(h.f.shown)
	h.send(tea.FocusMsg{})
	h.m.notified, h.m.notifiedAt = "b", t0.Add(-2*time.Minute) // too long ago
	h.send(tea.FocusMsg{})
	h.m.notified, h.m.notifiedAt = "gone", t0
	h.send(tea.FocusMsg{})
	if len(h.f.shown) != n {
		t.Errorf("focus without a fresh notification switched: %v", h.f.shown[n:])
	}
	_ = core.StatusDone
}
