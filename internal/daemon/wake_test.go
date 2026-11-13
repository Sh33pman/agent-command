package daemon

import (
	"bytes"
	"context"
	"errors"
	"log"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// fakeWaker is the Mac's power schedule in memory, so tests never touch the
// real one.
type fakeWaker struct {
	mu      sync.Mutex
	allowed bool
	wakes   []time.Time
	log     []string
	fail    bool
	awake   int // KeepAwake holds
	stopped int
}

func (f *fakeWaker) Allowed(context.Context) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, "allowed?")
	return f.allowed
}

func (f *fakeWaker) Scheduled(context.Context) ([]time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.wakes), nil
}

func (f *fakeWaker) Set(_ context.Context, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("sudo: a password is required")
	}
	f.log = append(f.log, "set "+at.UTC().Format("01-02 15:04:05"))
	f.wakes = append(f.wakes, at)
	return nil
}

func (f *fakeWaker) Cancel(_ context.Context, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, "cancel "+at.UTC().Format("01-02 15:04:05"))
	f.wakes = slices.DeleteFunc(f.wakes, at.Equal)
	return nil
}

func (f *fakeWaker) KeepAwake(context.Context, time.Duration) (func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return nil, errors.New("no caffeinate")
	}
	f.awake++
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.stopped++
	}, nil
}

// took returns what was done since the last call.
func (f *fakeWaker) took() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.log
	f.log = nil
	return out
}

func TestWakeFollowsTheNextRunThatWantsIt(t *testing.T) {
	d, _, clk, _ := jobDaemon(t)
	fw := &fakeWaker{}
	d.cfg.Waker = fw
	ctx := context.Background()
	dir := t.TempDir()

	daily := folderJob(dir) // 08:00 every day
	daily.Wake = true
	job := mustSave(t, d, daily)

	// Without the rule nothing is set, and the state says so.
	d.syncWake(ctx, true)
	if took := fw.took(); !slices.Equal(took, []string{"allowed?"}) {
		t.Errorf("no rule: %v", took)
	}
	if st := d.wakeState(ctx); st == nil || st.Allowed || !st.Next.IsZero() {
		t.Errorf("no rule: %+v", st)
	}

	// With it, one wake two minutes before the run.
	fw.allowed = true
	fw.wakes = []time.Time{time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC)} // a stale one of ours
	st, err := d.setWake(ctx, ipc.WakeRef{})
	must(t, err)
	if took := fw.took(); !slices.Equal(took, []string{"allowed?", "cancel 11-01 06:00:00", "set 11-12 07:58:00"}) {
		t.Errorf("rule: %v", took)
	}
	if !st.Allowed || st.All || !st.Next.Equal(time.Date(2026, 11, 12, 7, 58, 0, 0, time.UTC)) {
		t.Errorf("state: %+v", st)
	}

	// Nothing changes, nothing is run.
	d.syncWake(ctx, false)
	if took := fw.took(); len(took) != 0 {
		t.Errorf("unchanged: %v", took)
	}

	// A job that doesn't ask to wake doesn't, until every job does.
	other := folderJob(dir)
	other.Name, other.Schedule = "Early", "0 7 * * *"
	mustSave(t, d, other)
	clk.set("2026-11-12 07:30:00") // past 07:00 today
	d.syncWake(ctx, false)
	if took := fw.took(); len(took) != 0 {
		t.Errorf("a job that doesn't wake: %v", took)
	}
	_, err = d.setWake(ctx, ipc.WakeRef{All: true})
	must(t, err)
	if took := fw.took(); !slices.Equal(took, []string{"allowed?"}) {
		t.Errorf("all, but 07:00 has passed and 08:00 is still next: %v", took)
	}

	// After the 08:00 run, the next wake is for 07:00 tomorrow.
	clk.set("2026-11-12 08:00:10")
	d.runDueJobs(ctx)
	d.syncWake(ctx, false)
	if took := fw.took(); !slices.Equal(took, []string{"cancel 11-12 07:58:00", "set 11-13 06:58:00"}) {
		t.Errorf("after the run: %v", took)
	}

	// Pausing the jobs, or deleting them, clears it.
	must(t, d.store.SetJobPaused(ctx, job.ID, true))
	_, err = d.setWake(ctx, ipc.WakeRef{})
	must(t, err)
	if took := fw.took(); !slices.Equal(took, []string{"allowed?", "cancel 11-13 06:58:00"}) || len(fw.wakes) != 0 {
		t.Errorf("paused: %v", took)
	}

	// A run due too soon to sleep through gets no wake.
	clk.set("2026-11-13 07:58:45")
	must(t, d.store.SetJobPaused(ctx, job.ID, false))
	d.syncWake(ctx, false)
	if took := fw.took(); len(took) != 0 {
		t.Errorf("too soon: %v", took)
	}
}

func TestWakeErrorsAreLoggedOnceAndRetried(t *testing.T) {
	d, _, _, _ := jobDaemon(t)
	var logs bytes.Buffer
	d.cfg.Log = log.New(&logs, "", 0)
	fw := &fakeWaker{allowed: true, fail: true}
	d.cfg.Waker = fw
	ctx := context.Background()
	j := folderJob(t.TempDir())
	j.Wake = true
	mustSave(t, d, j)

	d.syncWake(ctx, false)
	d.syncWake(ctx, false)
	if n := strings.Count(logs.String(), "wake: sudo: a password is required"); n != 1 {
		t.Errorf("logged %d times:\n%s", n, logs.String())
	}
	d.mu.Lock()
	st := d.wakeState(ctx)
	d.mu.Unlock()
	if st.Error == "" {
		t.Error("no error in the state")
	}
	if took := fw.took(); !slices.Equal(took, []string{"allowed?", "allowed?"}) {
		t.Errorf("checks the rule again after a failure: %v", took)
	}
	fw.fail = false
	d.syncWake(ctx, false)
	if took := fw.took(); !slices.Equal(took, []string{"allowed?", "set 11-12 07:58:00"}) {
		t.Errorf("recovers: %v", took)
	}
	d.mu.Lock()
	st = d.wakeState(ctx)
	d.mu.Unlock()
	if st.Error != "" {
		t.Errorf("error kept: %q", st.Error)
	}
}

func TestWokenRunsKeepTheMacAwake(t *testing.T) {
	d, _, clk, _ := jobDaemon(t)
	fw := &fakeWaker{allowed: true}
	d.cfg.Waker = fw
	ctx := context.Background()
	j := folderJob(t.TempDir())
	j.Wake = true
	job := mustSave(t, d, j)
	plain := folderJob(t.TempDir())
	plain.Name = "Plain"
	mustSave(t, d, plain)

	clk.set("2026-11-12 08:00:05")
	d.runDueJobs(ctx)
	if fw.awake != 1 {
		t.Fatalf("holds: %d", fw.awake)
	}
	run := lastRun(t, d, job.ID)
	d.keepAwake(ctx, run.SessionID) // the same run again: still one
	if fw.awake != 1 {
		t.Errorf("held twice: %d", fw.awake)
	}

	// Busy past the grace period: still held. Done: released.
	s, err := d.store.Session(ctx, run.SessionID)
	must(t, err)
	s.Status = core.StatusBusy
	must(t, d.store.UpdateSession(ctx, s))
	clk.set("2026-11-12 08:01:00")
	d.releaseAwake(ctx, false)
	clk.set("2026-11-12 08:05:00")
	d.releaseAwake(ctx, false)
	if fw.stopped != 0 {
		t.Errorf("released while busy: %d", fw.stopped)
	}
	s.Status = core.StatusDone
	must(t, d.store.UpdateSession(ctx, s))
	d.releaseAwake(ctx, false)
	if fw.stopped != 1 {
		t.Errorf("not released when done: %d", fw.stopped)
	}

	// The cap, and the daemon stopping, release them too.
	d.keepAwake(ctx, "other")
	clk.set("2026-11-12 10:06:00")
	d.releaseAwake(ctx, false)
	d.keepAwake(ctx, "another")
	d.releaseAwake(ctx, true)
	if fw.stopped != 3 {
		t.Errorf("stopped: %d", fw.stopped)
	}
	fw.fail = true
	d.keepAwake(ctx, "fails")
	d.keepAwake(ctx, "")
	if len(d.wake.awake) != 0 {
		t.Errorf("held: %v", d.wake.awake)
	}
}

func TestWakeWithoutAWaker(t *testing.T) {
	d, _, _, _ := jobDaemon(t)
	ctx := context.Background()
	d.syncWake(ctx, true)
	d.keepAwake(ctx, "s")
	if st := d.wakeState(ctx); st != nil {
		t.Errorf("state: %+v", st)
	}
	st, err := d.setWake(ctx, ipc.WakeRef{All: true})
	if err != nil || !st.All || st.Allowed {
		t.Errorf("set: %+v %v", st, err)
	}
}

func TestWakeOpAndSnapshot(t *testing.T) {
	p := testPaths(t)
	fw := &fakeWaker{allowed: true}
	start(t, p, func(c *Config) { c.Waker = fw; c.ScheduleEvery = time.Hour })
	c := dial(t, p)
	var st ipc.WakeState
	must(t, c.Call(ipc.OpWakeSet, ipc.WakeRef{All: true}, &st))
	if !st.Allowed || !st.All {
		t.Errorf("set: %+v", st)
	}
	if snap := snapshot(t, c); snap.Wake == nil || !snap.Wake.All || !snap.Wake.Allowed {
		t.Errorf("snapshot: %+v", snap.Wake)
	}
	if err := c.Call(ipc.OpWakeSet, "no", nil); err == nil {
		t.Error("bad request")
	}
}

func TestWakeLooksForTheRuleNowAndThen(t *testing.T) {
	d, _, clk, _ := jobDaemon(t)
	fw := &fakeWaker{}
	d.cfg.Waker = fw
	ctx := context.Background()
	j := folderJob(t.TempDir())
	j.Wake = true
	mustSave(t, d, j)
	d.syncWake(ctx, false)
	d.syncWake(ctx, false)
	if took := fw.took(); !slices.Equal(took, []string{"allowed?"}) {
		t.Errorf("checks once: %v", took)
	}
	// Added from elsewhere (acmd init, say): found within wakeRecheck.
	fw.allowed = true
	clk.set("2026-11-12 07:06:00")
	d.syncWake(ctx, false)
	if took := fw.took(); !slices.Equal(took, []string{"allowed?", "set 11-12 07:58:00"}) {
		t.Errorf("found it: %v", took)
	}
	// Once on, it doesn't keep asking sudo.
	clk.set("2026-11-12 07:30:00")
	d.syncWake(ctx, false)
	if took := fw.took(); len(took) != 0 {
		t.Errorf("asked again: %v", took)
	}
}
