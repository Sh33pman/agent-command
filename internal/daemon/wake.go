package daemon

import (
	"context"
	"sync"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/schedule"
	"github.com/brettmostert/agent-command/internal/wake"
)

const (
	// wakeLead is how long before a run the Mac wakes: time for the network
	// to come back before the run starts.
	wakeLead = 2 * time.Minute
	// keepAwakeFor is the longest a woken run holds off idle sleep, and
	// keepAwakeGrace how long a run gets to get busy before its session
	// counts as done.
	keepAwakeFor   = 2 * time.Hour
	keepAwakeGrace = 2 * time.Minute
	// keyWakeAll is the app_state key for waking for every job.
	keyWakeAll = "jobs.wake_all"
	// wakeRecheck is how often, while waking is off, the daemon looks for the
	// sudoers rule again.
	wakeRecheck = 5 * time.Minute
)

// waking is what the daemon knows about the Mac's wakes. syncMu serializes
// the slow part (pmset and sudo); mu guards the fields and is only held
// briefly, so a snapshot never waits on pmset. Lock order: d.mu, then mu.
type waking struct {
	syncMu    sync.Mutex
	mu        sync.Mutex
	checked   bool      // allowed is known
	checkedAt time.Time // when it was last checked
	allowed   bool      // the sudoers rule is in place
	synced    bool      // the wake at `at` is set
	at        time.Time // the wake acmd set (zero: none)
	lastErr   string
	awake     map[string]awakeRun // by session: woken runs holding off sleep
}

type awakeRun struct {
	since time.Time
	stop  func()
}

// wantsWake says whether j's runs wake the Mac.
func wantsWake(j *core.Job, all bool) bool {
	return !j.Paused && (all || j.Wake)
}

// nextWake is when to wake for the soonest run that wants it: wakeLead
// before it, and only if that's still ahead (zero: none).
func (d *Daemon) nextWake(jobs []*core.Job, all bool) time.Time {
	now := d.cfg.Now()
	var best time.Time
	for _, j := range jobs {
		if !wantsWake(j, all) {
			continue
		}
		from := j.LastDue
		if now.After(from) {
			from = now
		}
		next := schedule.Schedule{Cron: j.Cron, Once: j.Once}.Next(from, d.cfg.Location)
		if next.IsZero() {
			continue
		}
		at := next.Add(-wakeLead)
		if at.Before(now.Add(30 * time.Second)) {
			continue // too soon to sleep through
		}
		if best.IsZero() || at.Before(best) {
			best = at
		}
	}
	return best.Truncate(time.Second)
}

// syncWake sets the Mac's wake for the next run that wants one, changing
// it only when that time changes. force checks the sudoers rule again and
// reads the power schedule afresh.
func (d *Daemon) syncWake(ctx context.Context, force bool) {
	w := d.cfg.Waker
	if w == nil {
		return
	}
	d.wake.syncMu.Lock()
	defer d.wake.syncMu.Unlock()

	d.mu.Lock()
	jobs, err := d.store.Jobs(ctx)
	all, _ := d.store.AppState(ctx, keyWakeAll)
	d.mu.Unlock()
	if err != nil {
		return
	}

	d.wake.mu.Lock()
	// While it's off, look for the rule now and then: it may have been added
	// from elsewhere.
	check := force || !d.wake.checked || (!d.wake.allowed && d.cfg.Now().Sub(d.wake.checkedAt) > wakeRecheck)
	d.wake.mu.Unlock()
	if check {
		allowed := w.Allowed(ctx)
		d.wake.mu.Lock()
		d.wake.allowed, d.wake.checked, d.wake.checkedAt = allowed, true, d.cfg.Now()
		d.wake.mu.Unlock()
	}

	d.wake.mu.Lock()
	allowed, synced, at := d.wake.allowed, d.wake.synced, d.wake.at
	d.wake.mu.Unlock()
	if !allowed {
		// Without the rule there's nothing acmd can set or cancel.
		d.setWakeFields(time.Time{}, false, "")
		return
	}
	want := d.nextWake(jobs, all == "1")
	if !force && synced && want.Equal(at) {
		return
	}
	if err := wake.Sync(ctx, w, want); err != nil {
		d.wake.mu.Lock()
		d.wake.synced, d.wake.checked = false, false // check the rule again next time
		msg := err.Error()
		if msg != d.wake.lastErr {
			d.cfg.Log.Printf("wake: %s", msg)
			d.wake.lastErr = msg
		}
		d.wake.mu.Unlock()
		return
	}
	d.setWakeFields(want, true, "")
	if !want.Equal(at) {
		d.changes.bump() // the app shows the next wake
	}
}

func (d *Daemon) setWakeFields(at time.Time, synced bool, lastErr string) {
	d.wake.mu.Lock()
	defer d.wake.mu.Unlock()
	d.wake.at, d.wake.synced, d.wake.lastErr = at, synced, lastErr
}

// keepAwake holds off idle sleep while a woken run's session works.
func (d *Daemon) keepAwake(ctx context.Context, sessionID string) {
	w := d.cfg.Waker
	if w == nil || sessionID == "" {
		return
	}
	d.wake.mu.Lock()
	_, running := d.wake.awake[sessionID]
	d.wake.mu.Unlock()
	if running {
		return
	}
	stop, err := w.KeepAwake(ctx, keepAwakeFor)
	if err != nil {
		d.cfg.Log.Printf("wake: keeping the Mac awake: %v", err)
		return
	}
	d.wake.mu.Lock()
	defer d.wake.mu.Unlock()
	if d.wake.awake == nil {
		d.wake.awake = map[string]awakeRun{}
	}
	d.wake.awake[sessionID] = awakeRun{since: d.cfg.Now(), stop: stop}
}

// releaseAwake lets the Mac sleep again once a woken run's session is done
// (or gone, or past keepAwakeFor). all releases every one, as the daemon stops.
func (d *Daemon) releaseAwake(ctx context.Context, all bool) {
	now := d.cfg.Now()
	d.wake.mu.Lock()
	runs := make(map[string]awakeRun, len(d.wake.awake))
	for id, r := range d.wake.awake {
		runs[id] = r
	}
	d.wake.mu.Unlock()

	for id, r := range runs {
		done := all || now.Sub(r.since) > keepAwakeFor
		if !done && now.Sub(r.since) > keepAwakeGrace {
			d.mu.Lock()
			s, err := d.store.Session(ctx, id)
			d.mu.Unlock()
			done = err != nil || !busy(s.Status)
		}
		if done {
			r.stop()
			d.wake.mu.Lock()
			delete(d.wake.awake, id)
			d.wake.mu.Unlock()
		}
	}
}

// wakeState is the wake setting and what's set, for the snapshot (d.mu is
// held; nil when acmd can't wake this Mac).
func (d *Daemon) wakeState(ctx context.Context) *ipc.WakeState {
	if d.cfg.Waker == nil {
		return nil
	}
	all, _ := d.store.AppState(ctx, keyWakeAll)
	d.wake.mu.Lock()
	defer d.wake.mu.Unlock()
	return &ipc.WakeState{Allowed: d.wake.allowed, All: all == "1", Next: d.wake.at, Error: d.wake.lastErr}
}

// setWake turns waking for every job on or off, checks the rule again and
// moves the wake to match.
func (d *Daemon) setWake(ctx context.Context, ref ipc.WakeRef) (*ipc.WakeState, error) {
	v := ""
	if ref.All {
		v = "1"
	}
	d.mu.Lock()
	err := d.store.SetAppState(ctx, keyWakeAll, v)
	d.mu.Unlock()
	if err != nil {
		return nil, err
	}
	d.syncWake(ctx, true)
	d.changes.bump()
	d.mu.Lock()
	defer d.mu.Unlock()
	if st := d.wakeState(ctx); st != nil {
		return st, nil
	}
	return &ipc.WakeState{All: ref.All}, nil
}
