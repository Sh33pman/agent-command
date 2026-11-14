package daemon

import (
	"time"

	"github.com/brettmostert/agent-command/internal/core"
)

// Background work: Claude can start a command or an agent in the background and
// end its turn straight away. When the work finishes, Claude wakes up for a short
// new turn. Neither turn is long enough for a "finished" notification, so a
// 10-minute build would end without one. The daemon follows that work from the
// hooks, so the turn Claude wakes up for counts from when the work started, and
// the row says it's still running.

// BackgroundDetail is a done row's detail while its background work runs.
const BackgroundDetail = "background work"

// backgroundKeep bounds how long work is followed: one that's killed may never
// wake Claude up.
const backgroundKeep = 12 * time.Hour

type background struct {
	running map[string]map[string]time.Time // session -> work id -> when its turn started
	since   map[string]time.Time            // session -> when finished, still unreported work started
}

func newBackground() *background {
	return &background{running: map[string]map[string]time.Time{}, since: map[string]time.Time{}}
}

// work is d.bg, made on first use (d.mu is held).
func (d *Daemon) work() *background {
	if d.bg == nil {
		d.bg = newBackground()
	}
	return d.bg
}

// follow records what event e says about s's background work (d.mu is held).
// Call it after core.Apply, before the transition.
func (d *Daemon) follow(s *core.Session, e core.Event) {
	b := d.work()
	switch {
	case e.Kind == core.HookSessionStart && e.Source != "compact", e.Kind == core.HookSessionEnd, s.Stopped(),
		s.Status == core.StatusArchived:
		// A new or ended Claude process: its work went with it.
		delete(b.running, s.ID)
		delete(b.since, s.ID)
		return
	}
	now := d.cfg.Now()
	for id, at := range b.running[s.ID] {
		if now.Sub(at) > backgroundKeep {
			delete(b.running[s.ID], id)
		}
	}
	if e.BackgroundTask != "" {
		start := e.Time
		if n := d.notify; n != nil {
			if t, ok := n.busySince[s.ID]; ok {
				start = t // the turn that started it
			}
		}
		if b.running[s.ID] == nil {
			b.running[s.ID] = map[string]time.Time{}
		}
		b.running[s.ID][e.BackgroundTask] = start
	}
	if e.Wakeup {
		if start, ok := b.running[s.ID][e.TaskDone]; ok {
			delete(b.running[s.ID], e.TaskDone)
			if t, ok := b.since[s.ID]; !ok || start.Before(t) {
				b.since[s.ID] = start
			}
		}
	}
	if len(b.running[s.ID]) == 0 {
		delete(b.running, s.ID)
	}
}

// workStarted is when the work a finished turn reports started: the turn's own
// start, or earlier, when it's Claude waking up for background work. It's used
// once (d.mu is held).
func (d *Daemon) workStarted(s *core.Session, turn time.Time, ok bool) (time.Time, bool) {
	b := d.work()
	since, found := b.since[s.ID]
	delete(b.since, s.ID)
	if found && (!ok || since.Before(turn)) {
		return since, true
	}
	return turn, ok
}

// showBackground marks done rows whose background work still runs (d.mu is held).
// Only the snapshot shows it, so a restarted daemon has nothing stale to clear.
func (d *Daemon) showBackground(sessions []*core.Session) {
	for _, s := range sessions {
		if s.Status == core.StatusDone && s.StatusDetail == "" && len(d.work().running[s.ID]) > 0 {
			s.StatusDetail = BackgroundDetail
		}
	}
}
