package daemon

import (
	"path/filepath"
	"time"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// Notifications: the daemon decides when to notify, and the app
// shows the notification, so a click lands on the session. With the app closed,
// the daemon shows it itself through Config.Fallback.

const keepNotices = 50

// appGap is how long after a watch ends the app still counts as running: it starts
// the next one straight after reading what changed.
const appGap = 5 * time.Second

type notifier struct {
	cfg       config.Notify
	doneMin   time.Duration
	cooldown  time.Duration
	seq       uint64
	notices   []ipc.Notice         // recent, oldest first
	busySince map[string]time.Time // when each session's current turn started
	last      map[string]time.Time // last notification per session (cooldown)
	waiting   map[string]bool      // a waiting notice is live (withdraw when it moves on)
}

func newNotifier(cfg config.Notify) *notifier {
	n := &notifier{cfg: cfg, busySince: map[string]time.Time{}, last: map[string]time.Time{}, waiting: map[string]bool{}}
	n.doneMin, n.cooldown = cfg.Durations()
	return n
}

// transition records a status change of s and returns the notices it
// causes (d.mu is held).
func (d *Daemon) transition(s *core.Session, tr core.Transition) {
	n := d.notify
	if n == nil || !tr.Changed() {
		return
	}
	now := d.cfg.Now()
	if tr.To == core.StatusBusy {
		n.busySince[s.ID] = now
	}
	var kind string
	switch tr.To {
	case core.StatusWaiting:
		if n.cfg.OnWaiting {
			kind = "waiting"
		}
	case core.StatusError:
		if n.cfg.OnError {
			kind = "error"
		}
	case core.StatusDone:
		since, ok := n.busySince[s.ID]
		since, ok = d.workStarted(s, since, ok)
		if ok && n.cfg.OnDone && now.Sub(since) >= n.doneMin {
			kind = "done"
		}
	case core.StatusPaused:
		if n.cfg.OnPaused {
			kind = "paused"
		}
	}
	if tr.From == core.StatusWaiting && n.waiting[s.ID] {
		delete(n.waiting, s.ID)
		d.addNotice(ipc.Notice{SessionID: s.ID, Kind: "withdraw"})
	}
	if tr.To != core.StatusBusy {
		delete(n.busySince, s.ID)
	}
	if kind == "" || !n.cfg.Enabled {
		return
	}
	if last, ok := n.last[s.ID]; ok && now.Sub(last) < n.cooldown {
		return
	}
	n.last[s.ID] = now
	name := s.Name
	if name == "" && s.Cwd != "" {
		name = filepath.Base(s.Cwd)
	}
	if name == "" {
		name = "session " + s.BgID
	}
	no := ipc.Notice{SessionID: s.ID, Kind: kind, Sound: n.cfg.Sound}
	switch kind {
	case "waiting":
		no.Title, no.Body, no.Urgent = "⚠ "+name+" needs you", firstNonEmpty(s.StatusDetail, "waiting for you"), true
		n.waiting[s.ID] = true
	case "error":
		no.Title, no.Body, no.Urgent = "✗ "+name+" failed", firstNonEmpty(s.StatusDetail, "error"), true
	case "done":
		no.Title, no.Body = "✓ "+name+" finished", filepath.Base(s.Cwd)
	case "paused":
		no.Title, no.Body = "‖ "+name+" stopped", filepath.Base(s.Cwd)
	}
	d.addNotice(no)
}

// addNotice picks the emitter and records the notice; without one the
// fallback shows it now.
func (d *Daemon) addNotice(no ipc.Notice) {
	n := d.notify
	n.seq++
	no.Seq = n.seq
	no.Emitter = d.emitter()
	n.notices = append(n.notices, no)
	if len(n.notices) > keepNotices {
		n.notices = n.notices[len(n.notices)-keepNotices:]
	}
	if no.Emitter == "" && no.Kind != "withdraw" && d.cfg.Fallback != nil {
		go d.cfg.Fallback(no)
	}
}

// emitter is the app if it's running; otherwise the daemon shows it itself.
func (d *Daemon) emitter() string {
	if d.appRunning() {
		return ipc.EmitterApp
	}
	return ""
}

// appRunning reports whether the app is watching, or was a moment ago.
func (d *Daemon) appRunning() bool {
	if d.appWatches.Load() > 0 {
		return true
	}
	last := d.appLastWatch.Load()
	return last != 0 && d.cfg.Now().Sub(time.Unix(0, last)) < appGap
}

func (d *Daemon) notices() []ipc.Notice {
	if d.notify == nil {
		return nil
	}
	return append([]ipc.Notice(nil), d.notify.notices...)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
