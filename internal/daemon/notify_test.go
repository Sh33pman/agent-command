package daemon

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"slices"
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
	if len(n) != 1 || n[0].Kind != "waiting" || !n[0].Urgent || n[0].Title != "⚠ auth needs you" || n[0].Body != "permission: Bash" || len(n[0].To) != 0 {
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
	if n := d.notices(); len(n) != 1 || n[0].Kind != "paused" || n[0].Title != "‖ session s1 stopped" {
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

// connect registers a client as its hello would, and returns how to close it.
func connect(t *testing.T, d *Daemon, id, kind string, caps ...string) (*caller, func()) {
	t.Helper()
	who := &caller{}
	return who, d.hello(context.Background(), &ipc.ClientHello{ID: id, Kind: kind, Device: "laptop", Caps: caps}, who)
}

func use(t *testing.T, d *Daemon, who *caller, presence string) {
	t.Helper()
	must(t, d.presence(context.WithValue(context.Background(), callerKey{}, who), ipc.ClientPresence{Presence: presence, Focus: "s1"}))
}

// notify.route = one: the client you're using, else the desktop client used
// last, else the host's own notification. all: every connected one.
func TestNoticesGoWhereYouAre(t *testing.T) {
	d, clock, fallback := notifyDaemon(t, config.Default().Notify)
	must(t, d.loadClients(context.Background()))
	s := &core.Session{ID: "s1", Name: "auth", Cwd: "/w/api"}
	notice := func() ipc.Notice {
		t.Helper()
		*clock = clock.Add(time.Minute) // past the cooldown
		d.transition(s, core.Transition{From: core.StatusBusy, To: core.StatusDone})
		d.transition(s, core.Transition{From: core.StatusDone, To: core.StatusError})
		n := d.notices()
		return n[len(n)-1]
	}
	if n := notice(); len(n.To) != 0 {
		t.Fatalf("nobody there: %+v", n)
	}
	waitFor(t, "the host's own notification", func() bool { return len(fallback()) == 1 })
	mac, closeMac := connect(t, d, "mac-1", ipc.KindMac, ipc.CapNotify, ipc.CapSound)
	linux, closeLinux := connect(t, d, "linux-1", ipc.KindLinux, ipc.CapNotify)
	connect(t, d, "phone", ipc.KindAndroid, ipc.CapNotify)
	connect(t, d, "script", ipc.KindCLI) // doesn't show notices
	if n := notice(); !slices.Equal(n.To, []string{"linux-1"}) && !slices.Equal(n.To, []string{"mac-1"}) {
		t.Errorf("no one active: a desktop client, got %v", n.To)
	}
	use(t, d, mac, ipc.PresenceActive)
	*clock = clock.Add(time.Second)
	use(t, d, linux, ipc.PresenceActive)
	if n := notice(); !slices.Equal(n.To, []string{"linux-1"}) {
		t.Errorf("the one used last: %v", n.To)
	}
	use(t, d, linux, ipc.PresenceIdle)
	use(t, d, mac, ipc.PresenceActive)
	if n := notice(); !slices.Equal(n.To, []string{"mac-1"}) {
		t.Errorf("the active one: %v", n.To)
	}
	d.notify.cfg.Route = config.RouteAll
	if n := notice(); !slices.Equal(n.To, []string{"linux-1", "mac-1", "phone"}) {
		t.Errorf("all: %v", n.To)
	}
	before := len(fallback())
	closeMac()
	closeLinux()
	*clock = clock.Add(clientGap + time.Second)
	d.notify.cfg.Route = config.RouteOne
	if n := notice(); len(n.To) != 0 {
		t.Errorf("only a phone, which comes later: %v", n.To)
	}
	waitFor(t, "the host's own notification", func() bool { return len(fallback()) == before+1 })
	// Notices by id.
	last := d.notices()[len(d.notices())-1]
	if got, err := d.noticeByID(ipc.NoticeRef{ID: last.Seq}); err != nil || got.Seq != last.Seq {
		t.Errorf("by id: %+v %v", got, err)
	}
	if _, err := d.noticeByID(ipc.NoticeRef{ID: 9999}); resultOf("", err).Code != ipc.CodeNotFound {
		t.Errorf("no such notice: %v", err)
	}
}

// The registry says who's there and how they're used; ones not seen for
// clients.forget_after are forgotten, unless it's 0.
func TestTheClientRegistry(t *testing.T) {
	d, clock, _ := notifyDaemon(t, config.Default().Notify)
	ctx := context.Background()
	must(t, d.loadClients(ctx))
	mac, closeMac := connect(t, d, "mac-1", ipc.KindMac, ipc.CapNotify)
	use(t, d, mac, ipc.PresenceBackground)
	if err := d.presence(context.WithValue(ctx, callerKey{}, mac), ipc.ClientPresence{Presence: "asleep"}); err == nil {
		t.Error("asleep")
	}
	if err := d.presence(ctx, ipc.ClientPresence{Presence: ipc.PresenceActive}); err == nil {
		t.Error("presence from no one")
	}
	if d.hello(ctx, &ipc.ClientHello{Kind: ipc.KindCLI}, &caller{}); len(d.clientList()) != 1 {
		t.Error("a client without an id was registered")
	}
	list := d.clientList()
	if len(list) != 1 || list[0].Presence != ipc.PresenceBackground || !list[0].Connected || list[0].Focus != "s1" || list[0].Device != "laptop" {
		t.Errorf("clients = %+v", list)
	}
	closeMac()
	*clock = clock.Add(time.Minute)
	if list = d.clientList(); list[0].Connected || list[0].Presence != ipc.PresenceDisconnected {
		t.Errorf("gone: %+v", list)
	}
	// It's remembered, as the store has it.
	must(t, d.loadClients(ctx))
	if list = d.clientList(); len(list) != 1 || list[0].Kind != ipc.KindMac {
		t.Errorf("reloaded: %+v", list)
	}
	d.cfg.ConfigFile = filepath.Join(t.TempDir(), "config.toml")
	_, err := d.settings().Set(map[string]any{"clients.forget_after": "0"})
	must(t, err)
	*clock = clock.Add(365 * 24 * time.Hour)
	if n := d.forgetClients(ctx); n != 0 {
		t.Errorf("0 forgot %d", n)
	}
	_, err = d.settings().Set(map[string]any{"clients.forget_after": "720h"})
	must(t, err)
	if n := d.forgetClients(ctx); n != 1 || len(d.clientList()) != 0 {
		t.Errorf("forgot %d: %+v", n, d.clientList())
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
	if n := d.notices(); len(n) != 2 || !strings.Contains(logged.String(), "stay in effect") {
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
