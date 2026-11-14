package daemon

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/store"
)

func notifyDaemon(t *testing.T, cfg config.Notify) (*Daemon, *time.Time, func() []ipc.Notice) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	must(t, err)
	t.Cleanup(func() { st.Close() })
	clock := time.Date(2026, 11, 1, 2, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	var fallback []ipc.Notice
	d := &Daemon{cfg: Config{Now: func() time.Time { return clock }, Fallback: func(n ipc.Notice) {
		mu.Lock()
		fallback = append(fallback, n)
		mu.Unlock()
	}}, store: st, notify: newNotifier(cfg)}
	return d, &clock, func() []ipc.Notice {
		mu.Lock()
		defer mu.Unlock()
		return append([]ipc.Notice(nil), fallback...)
	}
}

func TestNotifyDecisions(t *testing.T) {
	d, clock, fallback := notifyDaemon(t, config.Default().Notify)
	s := &core.Session{ID: "s1", Name: "auth", Cwd: "/w/api", StatusDetail: "permission: Bash"}
	step := func(to core.Status, after time.Duration) {
		*clock = clock.Add(after)
		from := s.Status
		s.Status = to
		d.transition(s, core.Transition{From: from, To: to})
	}
	step(core.StatusBusy, 0)
	step(core.StatusWaiting, time.Second)
	n := d.notices()
	if len(n) != 1 || n[0].Kind != "waiting" || !n[0].Urgent || n[0].Title != "⚠ auth needs you" || n[0].Body != "permission: Bash" || n[0].Emitter != "" {
		t.Fatalf("waiting: %+v", n)
	}
	step(core.StatusBusy, time.Second) // answered: withdraw
	if n = d.notices(); n[len(n)-1].Kind != "withdraw" {
		t.Errorf("no withdraw: %+v", n)
	}
	step(core.StatusDone, 10*time.Second) // short turn: nothing
	if len(d.notices()) != 2 {
		t.Errorf("short turn notified: %+v", d.notices())
	}
	step(core.StatusBusy, time.Minute)
	step(core.StatusDone, 31*time.Second)
	if n = d.notices(); n[len(n)-1].Kind != "done" || n[len(n)-1].Title != "✓ auth finished" || n[len(n)-1].Body != "api" {
		t.Errorf("long turn: %+v", n[len(n)-1])
	}
	step(core.StatusError, time.Second) // within the cooldown
	if n = d.notices(); n[len(n)-1].Kind == "error" {
		t.Error("cooldown ignored")
	}
	step(core.StatusError, 0)
	step(core.StatusDone, 0)
	step(core.StatusError, 21*time.Second)
	if n = d.notices(); n[len(n)-1].Kind != "error" || !n[len(n)-1].Urgent {
		t.Errorf("error: %+v", n[len(n)-1])
	}
	// No views: the fallback showed the real notices (not withdraws).
	time.Sleep(50 * time.Millisecond)
	if got := fallback(); len(got) != 3 {
		t.Errorf("fallback got %d: %+v", len(got), got)
	}

	for range 60 {
		step(core.StatusWaiting, time.Minute)
		step(core.StatusBusy, 0)
	}
	if len(d.notices()) != keepNotices {
		t.Errorf("kept %d notices", len(d.notices()))
	}
}

func TestNotifyOff(t *testing.T) {
	cfg := config.Default().Notify
	cfg.Enabled, cfg.OnPaused = false, true
	d, _, _ := notifyDaemon(t, cfg)
	s := &core.Session{ID: "s1"}
	d.transition(s, core.Transition{From: core.StatusBusy, To: core.StatusWaiting})
	if len(d.notices()) != 0 {
		t.Error("notified while disabled")
	}
	cfg.Enabled = true
	d, _, _ = notifyDaemon(t, cfg)
	d.transition(s, core.Transition{From: core.StatusDone, To: core.StatusPaused})
	if n := d.notices(); len(n) != 1 || n[0].Kind != "paused" || n[0].Title != "‖ session  stopped" {
		t.Errorf("paused: %+v", n)
	}
	d.transition(s, core.Transition{From: core.StatusPaused, To: core.StatusPaused}) // no change
	if len(d.notices()) != 1 {
		t.Error("notified without a change")
	}
	plain := &Daemon{}
	plain.transition(s, core.Transition{From: core.StatusBusy, To: core.StatusWaiting}) // notifications not configured
	if plain.notices() != nil {
		t.Error("notices without a notifier")
	}
}

// With the app watching, it shows notices; once it's gone, it doesn't.
func TestAppRunning(t *testing.T) {
	clock := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	d := &Daemon{cfg: Config{Now: func() time.Time { return clock }}}
	if d.appRunning() {
		t.Error("no app yet")
	}
	d.appWatches.Add(1)
	if !d.appRunning() {
		t.Error("watching")
	}
	d.appLastWatch.Store(clock.UnixNano())
	d.appWatches.Add(-1)
	clock = clock.Add(appGap - time.Second)
	if !d.appRunning() {
		t.Error("between watches")
	}
	clock = clock.Add(2 * time.Second)
	if d.appRunning() {
		t.Error("should have gone")
	}
}

// The app shows notices while it runs, so the fallback doesn't too.
func TestNoticeGoesToTheApp(t *testing.T) {
	d, _, fallback := notifyDaemon(t, config.Default().Notify)
	d.appWatches.Add(1)
	s := &core.Session{ID: "s1", Name: "auth", Cwd: "/w/api"}
	d.transition(s, core.Transition{From: core.StatusBusy, To: core.StatusWaiting})
	n := d.notices()
	if len(n) != 1 || n[0].Emitter != ipc.EmitterApp {
		t.Fatalf("notices: %+v", n)
	}
	if got := fallback(); len(got) != 0 {
		t.Errorf("fallback: %+v", got)
	}
}

// A change to config.toml's [notify] applies without a restart, and one that
// doesn't parse keeps what was there.
func TestNotifyFollowsTheConfigFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.toml")
	write := func(text string, at time.Time) {
		t.Helper()
		must(t, os.WriteFile(file, []byte(text), 0o600))
		must(t, os.Chtimes(file, at, at))
	}
	start := time.Now().Add(-time.Hour)
	write("[notify]\nenabled = false\n", start)
	d, _, _ := notifyDaemon(t, config.Default().Notify)
	var logged strings.Builder
	d.cfg.ConfigFile, d.cfg.Log = file, log.New(&logged, "", 0)
	s := &core.Session{ID: "s1"}
	d.transition(s, core.Transition{From: core.StatusBusy, To: core.StatusWaiting})
	if len(d.notices()) != 0 {
		t.Fatal("notified with notifications turned off in the file")
	}

	write("[notify]\nenabled = true\ncooldown = \"0s\"\n", start.Add(time.Minute))
	d.transition(s, core.Transition{From: core.StatusWaiting, To: core.StatusError})
	if n := d.notices(); len(n) != 1 || n[0].Kind != "error" {
		t.Fatalf("after turning them on: %+v", n)
	}

	write("[notify\n", start.Add(2*time.Minute))
	d.transition(s, core.Transition{From: core.StatusError, To: core.StatusWaiting})
	if n := d.notices(); len(n) != 2 || !strings.Contains(logged.String(), "stay as they were") {
		t.Errorf("a broken file: %+v %q", n, logged.String())
	}

	// No notifier yet (the file didn't parse at start): one is made once it does.
	fresh := &Daemon{cfg: Config{Now: time.Now, ConfigFile: file, Log: log.New(&logged, "", 0)}}
	write("[notify]\nenabled = true\n", start.Add(3*time.Minute))
	fresh.transition(s, core.Transition{From: core.StatusBusy, To: core.StatusWaiting})
	if n := fresh.notices(); len(n) != 1 {
		t.Errorf("fresh: %+v", n)
	}
}
