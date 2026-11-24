package reconcile

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/claudecli"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/store"
)

var ctx = context.Background()

type listing struct {
	agents []claudecli.Agent
	err    error
}

func (l *listing) Agents(context.Context) ([]claudecli.Agent, error) { return l.agents, l.err }

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }
func ptr[T any](v T) *T                  { return &v }
func agent(sid, id, kind, status string, pid int) claudecli.Agent {
	a := claudecli.Agent{SessionID: sid, ID: id, Kind: kind, Name: "claude-name-" + id, Cwd: "/w/" + id}
	if status != "" {
		a.Status = ptr(status)
	}
	if pid > 0 {
		a.Pid = ptr(pid)
	}
	return a
}

func setup(t *testing.T) (*Reconciler, *store.Store, *listing, *clock) {
	t.Helper()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	l := &listing{}
	c := &clock{t: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)}
	alive := map[int]bool{}
	r := &Reconciler{Store: st, Agents: l, Now: c.now, Alive: func(pid int) bool { return alive[pid] }}
	return r, st, l, c
}

func create(t *testing.T, st *store.Store, s *core.Session) *core.Session {
	t.Helper()
	if err := st.CreateSession(ctx, s); err != nil {
		t.Fatal(err)
	}
	return s
}

func get(t *testing.T, st *store.Store, id string) *core.Session {
	t.Helper()
	s, err := st.Session(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func once(t *testing.T, r *Reconciler) Result {
	t.Helper()
	res, err := r.Once(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestCorrectsPidCwdAndName(t *testing.T) {
	r, st, l, _ := setup(t)
	s := create(t, st, &core.Session{ClaudeSessionID: "s1", Status: core.StatusDone, Name: "first prompt"})
	u := create(t, st, &core.Session{ClaudeSessionID: "s2", Status: core.StatusDone, Name: "mine", NameSource: core.NameUser})
	l.agents = []claudecli.Agent{agent("s1", "aaaaaaaa", "interactive", "idle", 11), agent("s2", "bbbbbbbb", "interactive", "idle", 12)}
	if res := once(t, r); res.Updated != 2 {
		t.Fatalf("updated %d", res.Updated)
	}
	got := get(t, st, s.ID)
	if got.Kind != core.KindHosted || got.Pid != 11 || got.Name != "claude-name-aaaaaaaa" || got.Cwd != "/w/aaaaaaaa" {
		t.Errorf("corrected session: %+v", got)
	}
	if got := get(t, st, u.ID); got.Name != "mine" {
		t.Errorf("user name overwritten: %q", got.Name)
	}
}

func TestStoppedIsPausedImmediately(t *testing.T) {
	r, st, l, _ := setup(t)
	s := create(t, st, &core.Session{ClaudeSessionID: "s1", Status: core.StatusDone, Pid: 5})
	l.agents = []claudecli.Agent{agent("s1", "aaaaaaaa", "background", "", 0)} // pid/status null (M0 C3)
	once(t, r)
	if got := get(t, st, s.ID); got.Status != core.StatusPaused || got.Pid != 0 {
		t.Errorf("stopped: %+v", got)
	}
}

func TestDisagreementWaitsForGrace(t *testing.T) {
	r, st, l, c := setup(t)
	s := create(t, st, &core.Session{ClaudeSessionID: "s1", Status: core.StatusBusy, Pid: 7})
	l.agents = []claudecli.Agent{agent("s1", "aaaaaaaa", "background", "idle", 7)} // e.g. Esc: no Stop hook

	once(t, r)
	if got := get(t, st, s.ID); got.Status != core.StatusBusy {
		t.Fatalf("changed before grace: %s", got.Status)
	}
	c.advance(3 * time.Second)
	once(t, r)
	if got := get(t, st, s.ID); got.Status != core.StatusBusy {
		t.Fatalf("changed before grace: %s", got.Status)
	}
	c.advance(3 * time.Second)
	once(t, r)
	if got := get(t, st, s.ID); got.Status != core.StatusDone {
		t.Fatalf("after grace: %s, want done", got.Status)
	}

	// Agreement in between resets the timer.
	got := get(t, st, s.ID)
	got.Status = core.StatusBusy
	if err := st.UpdateSession(ctx, got); err != nil {
		t.Fatal(err)
	}
	once(t, r) // disagree: timer starts
	l.agents[0].Status = ptr("busy")
	c.advance(4 * time.Second)
	once(t, r) // agree: timer cleared
	l.agents[0].Status = ptr("idle")
	c.advance(4 * time.Second)
	once(t, r) // disagree again: new timer
	if got := get(t, st, s.ID); got.Status != core.StatusBusy {
		t.Errorf("timer wasn't reset by agreement: %s", got.Status)
	}
}

func TestAgreement(t *testing.T) {
	cases := []struct {
		ours   core.Status
		theirs string
		want   bool
	}{
		{core.StatusBusy, "busy", true}, {core.StatusStarting, "busy", true}, {core.StatusDone, "busy", false},
		{core.StatusWaiting, "waiting", true}, {core.StatusBusy, "waiting", false},
		{core.StatusDone, "idle", true}, {core.StatusError, "idle", true}, {core.StatusWaiting, "idle", true}, {core.StatusBusy, "idle", false},
		{core.StatusPaused, "stopped", true}, {core.StatusDone, "stopped", false},
		{core.StatusDone, "something-new", true},
	}
	for _, c := range cases {
		if got := agrees(c.ours, c.theirs); got != c.want {
			t.Errorf("agrees(%s, %s) = %v", c.ours, c.theirs, got)
		}
	}
}

// A session Claude doesn't list follows its host: paused once the host is
// gone, after the startup grace.
func TestUnlistedSessions(t *testing.T) {
	r, st, _, c := setup(t)
	fresh := create(t, st, &core.Session{ClaudeSessionID: "new", Status: core.StatusStarting, CreatedAt: c.t})
	alive := create(t, st, &core.Session{ClaudeSessionID: "alive", Status: core.StatusBusy, Pid: 100, CreatedAt: c.t.Add(-time.Hour)})
	gone := create(t, st, &core.Session{ClaudeSessionID: "gone", Status: core.StatusDone, Pid: 3, CreatedAt: c.t.Add(-time.Hour)})
	paused := create(t, st, &core.Session{ClaudeSessionID: "p", Status: core.StatusPaused})
	r.HostAlive = func(s *core.Session) bool { return s.ClaudeSessionID == "alive" }

	once(t, r)
	for id, want := range map[string]core.Status{fresh.ID: core.StatusStarting, alive.ID: core.StatusBusy,
		gone.ID: core.StatusPaused, paused.ID: core.StatusPaused} {
		if got := get(t, st, id); got.Status != want {
			t.Errorf("%s: %s, want %s", got.ClaudeSessionID, got.Status, want)
		}
	}
	if got := get(t, st, gone.ID); got.Pid != 0 {
		t.Errorf("gone host's pid not cleared: %d", got.Pid)
	}
	c.advance(time.Minute)
	once(t, r)
	if got := get(t, st, fresh.ID); got.Status != core.StatusPaused {
		t.Errorf("never started: %s, want paused after startup grace", got.Status)
	}
}

func TestListerError(t *testing.T) {
	r, _, l, _ := setup(t)
	l.err = errors.New("claude not found")
	if _, err := r.Once(ctx); err == nil {
		t.Error("expected error")
	}
}

// A hosted session is listed by Claude as interactive: that must not turn
// it into an external one, and leaving the listing must not pause it.
func TestHostedStaysHosted(t *testing.T) {
	r, st, l, _ := setup(t)
	s := create(t, st, &core.Session{ClaudeSessionID: "h1", Kind: core.KindHosted, Status: core.StatusDone})
	l.agents = []claudecli.Agent{agent("h1", "", "interactive", "busy", 11)}
	once(t, r)
	if got := get(t, st, s.ID); got.Kind != core.KindHosted {
		t.Errorf("kind %v", got.Kind)
	}
	l.agents = nil
	r.Now = func() time.Time { return time.Now().Add(time.Hour) }
	once(t, r)
	if got := get(t, st, s.ID); got.Status == core.StatusPaused {
		t.Errorf("unlisted hosted session paused: %v", got.Status)
	}
}

// A hosted session whose host is gone (reboot, crash) is paused once it is
// past the startup grace; one whose host runs is left alone.
func TestHostedPausedWhenHostGone(t *testing.T) {
	r, st, l, c := setup(t)
	alive := true
	r.HostAlive = func(*core.Session) bool { return alive }
	s := create(t, st, &core.Session{ClaudeSessionID: "h2", Kind: core.KindHosted, Status: core.StatusBusy, CreatedAt: c.now().Add(-time.Hour)})
	l.agents = nil
	once(t, r)
	if got := get(t, st, s.ID); got.Status != core.StatusBusy {
		t.Fatalf("host alive, status %v", got.Status)
	}
	alive = false
	once(t, r)
	if got := get(t, st, s.ID); got.Status != core.StatusPaused {
		t.Errorf("host gone, status %v", got.Status)
	}
	// A new session whose host hasn't listened yet is not paused.
	n := create(t, st, &core.Session{ClaudeSessionID: "h3", Kind: core.KindHosted, Status: core.StatusStarting, CreatedAt: c.now()})
	once(t, r)
	if got := get(t, st, n.ID); got.Status != core.StatusStarting {
		t.Errorf("new session, status %v", got.Status)
	}
}

func TestPauseDeadHostsNeedsNoListing(t *testing.T) {
	r, st, l, c := setup(t)
	alive := map[string]bool{"running": true}
	r.HostAlive = func(s *core.Session) bool { return alive[s.ClaudeSessionID] }
	l.err = errors.New("claude isn't available yet")
	old := c.now().Add(-time.Hour)
	gone := create(t, st, &core.Session{ClaudeSessionID: "gone", Kind: core.KindHosted, Status: core.StatusDone, CreatedAt: old})
	running := create(t, st, &core.Session{ClaudeSessionID: "running", Kind: core.KindHosted, Status: core.StatusBusy, CreatedAt: old})
	young := create(t, st, &core.Session{ClaudeSessionID: "young", Kind: core.KindHosted, Status: core.StatusStarting, CreatedAt: c.now()})

	n, err := r.PauseDeadHosts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("changed %d", n)
	}
	got := get(t, st, gone.ID)
	if got.Status != core.StatusPaused {
		t.Errorf("host gone, status %v", got.Status)
	}
	if got.ClaudeSessionID != "gone" {
		t.Errorf("the conversation to resume went too: %q", got.ClaudeSessionID)
	}
	if got := get(t, st, running.ID); got.Status != core.StatusBusy {
		t.Errorf("host running, status %v", got.Status)
	}
	if got := get(t, st, young.ID); got.Status != core.StatusStarting {
		t.Errorf("new session, status %v", got.Status)
	}
}

func TestTerminalAgentThatDiedReturnsToRunning(t *testing.T) {
	r, st, l, _ := setup(t)
	r.HostAlive = func(*core.Session) bool { return true }
	s := create(t, st, &core.Session{HarnessKind: core.HarnessTerminal, Kind: core.KindHosted,
		Status: core.StatusBusy, ClaudeSessionID: "a1", Pid: 77})
	// Claude's own listing doesn't decide a terminal's status.
	l.agents = []claudecli.Agent{agent("a1", "aaaaaaaa", "interactive", "", 0)}
	once(t, r)
	if got := get(t, st, s.ID); got.Status != core.StatusRunning || got.ClaudeSessionID != "" || got.Pid != 0 {
		t.Errorf("dead agent: %+v", got)
	}
}

func TestTerminalWithLiveAgentKeepsItsStatus(t *testing.T) {
	r, st, _, _ := setup(t)
	r.HostAlive = func(*core.Session) bool { return true }
	r.Alive = func(int) bool { return true }
	s := create(t, st, &core.Session{HarnessKind: core.HarnessTerminal, Kind: core.KindHosted,
		Status: core.StatusBusy, ClaudeSessionID: "a1", Pid: 77})
	once(t, r)
	if got := get(t, st, s.ID); got.Status != core.StatusBusy || got.ClaudeSessionID != "a1" {
		t.Errorf("live agent: %+v", got)
	}
}

// A dead host, as after a reboot, stops the terminal itself so it can come back.
func TestTerminalWithDeadHostStops(t *testing.T) {
	r, st, _, c := setup(t)
	r.HostAlive = func(*core.Session) bool { return false }
	r.StartupGrace = time.Minute
	s := create(t, st, &core.Session{HarnessKind: core.HarnessTerminal, Kind: core.KindHosted,
		Status: core.StatusBusy, ClaudeSessionID: "a1", Pid: 77, CreatedAt: c.now().Add(-time.Hour)})
	once(t, r)
	if got := get(t, st, s.ID); got.Status != core.StatusPaused || got.ClaudeSessionID != "" {
		t.Errorf("dead host: %+v", got)
	}
}

// Claude's listing never adds a row: the app only lists sessions it runs.
func TestListingDoesNotCreateSessions(t *testing.T) {
	r, st, l, _ := setup(t)
	l.agents = []claudecli.Agent{agent("s1", "aaaaaaaa", "background", "busy", 5)}
	once(t, r)
	if all, err := st.Sessions(ctx, true); err != nil || len(all) != 0 {
		t.Errorf("sessions: %+v, %v", all, err)
	}
}

// Only a running session that isn't a terminal is reconciled against Claude's
// listing. Without one, nothing asks for it.
func TestNeedsListing(t *testing.T) {
	r, st, _, c := setup(t)
	need := func() bool {
		t.Helper()
		ok, err := r.NeedsListing(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if need() {
		t.Error("no sessions: wants the listing")
	}
	create(t, st, &core.Session{ID: "term", HarnessKind: core.HarnessTerminal, Status: core.StatusRunning, CreatedAt: c.now()})
	create(t, st, &core.Session{ID: "old", ClaudeSessionID: "c-old", Kind: core.KindHosted, Status: core.StatusPaused, CreatedAt: c.now()})
	create(t, st, &core.Session{ID: "done", ClaudeSessionID: "c-done", Kind: core.KindHosted, Status: core.StatusFinished, CreatedAt: c.now()})
	if need() {
		t.Error("only a terminal and stopped sessions: wants the listing")
	}
	// Claude never lists agy.
	create(t, st, &core.Session{ID: "agy", HarnessKind: core.HarnessAntigravity, ClaudeSessionID: "conv", Kind: core.KindHosted, Status: core.StatusBusy, CreatedAt: c.now()})
	if need() {
		t.Error("only an Antigravity session running: wants the listing")
	}
	create(t, st, &core.Session{ID: "live", ClaudeSessionID: "c-live", Kind: core.KindHosted, Status: core.StatusBusy, CreatedAt: c.now()})
	if !need() {
		t.Error("a running session: doesn't want the listing")
	}
}

// Apply works from a listing fetched earlier, as the daemon does.
func TestFetchThenApplyIsOnce(t *testing.T) {
	r, st, l, _ := setup(t)
	create(t, st, &core.Session{ID: "a", ClaudeSessionID: "s1", Status: core.StatusBusy, CreatedAt: time.Now()})
	l.agents = []claudecli.Agent{agent("s1", "aaaaaaaa", "interactive", "busy", 9)}
	fetched, err := r.Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	l.agents = nil // the listing has moved on; Apply uses what was fetched
	if _, err := r.Apply(ctx, fetched); err != nil {
		t.Fatal(err)
	}
	if s := get(t, st, "a"); s.Pid != 9 || s.Name != "claude-name-aaaaaaaa" {
		t.Errorf("applied %+v", s)
	}
}

// An Antigravity session isn't in Claude's listing: its host says whether it
// runs, even when Claude lists a session with the same id.
func TestAntigravityFollowsItsHost(t *testing.T) {
	r, st, l, c := setup(t)
	alive := true
	r.HostAlive = func(*core.Session) bool { return alive }
	s := create(t, st, &core.Session{HarnessKind: core.HarnessAntigravity, ClaudeSessionID: "conv", Kind: core.KindHosted,
		Status: core.StatusBusy, CreatedAt: c.now().Add(-time.Hour)})
	l.agents = []claudecli.Agent{agent("conv", "aaaaaaaa", "background", "idle", 0)}
	once(t, r)
	if got := get(t, st, s.ID); got.Status != core.StatusBusy || got.Kind != core.KindHosted {
		t.Fatalf("host alive: %+v", got)
	}
	alive = false
	once(t, r)
	if got := get(t, st, s.ID); got.Status != core.StatusPaused {
		t.Errorf("host gone, status %v", got.Status)
	}
}
