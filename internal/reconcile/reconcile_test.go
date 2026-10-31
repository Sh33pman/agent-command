package reconcile

import (
	"context"
	"errors"
	"os"
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

func TestDiscoversBackgroundSessionsFromRecordedListing(t *testing.T) {
	r, st, l, _ := setup(t)
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "claudecli", "agents-mixed.json"))
	if err != nil {
		t.Fatal(err)
	}
	if l.agents, err = claudecli.ParseAgents(b); err != nil {
		t.Fatal(err)
	}
	if res := once(t, r); res.Discovered != 3 {
		t.Fatalf("discovered %d, want the 3 background sessions (not the interactive one)", res.Discovered)
	}
	want := map[string]core.Status{
		"9df2c810-bd4d-4db9-90ce-2ea000b7917c": core.StatusDone,
		"c95849b2-1a6b-4d75-b4a6-9160270b9fc9": core.StatusWaiting,
		"77d411e3-6d72-4c37-890f-76c3f97f965c": core.StatusPaused,
	}
	for sid, status := range want {
		s, err := st.SessionByClaudeID(ctx, sid)
		if err != nil {
			t.Fatalf("%s: %v", sid, err)
		}
		if s.Status != status || s.Kind != core.KindBackground || s.BgID != sid[:8] || s.Name == "" || s.Cwd != "/work/project" {
			t.Errorf("%s: %+v", sid[:8], s)
		}
	}
	// A second pass changes nothing.
	if res := once(t, r); res.Discovered != 0 || res.Updated != 0 {
		t.Errorf("second pass: %+v", res)
	}
}

func TestCorrectsKindIDsPidAndName(t *testing.T) {
	r, st, l, _ := setup(t)
	s := create(t, st, &core.Session{ClaudeSessionID: "s1", Kind: core.KindExternal, Status: core.StatusDone,
		External: &core.PaneLoc{Terminal: "kitty"}, Name: "first prompt"})
	u := create(t, st, &core.Session{ClaudeSessionID: "s2", Kind: core.KindBackground, Status: core.StatusDone,
		Name: "mine", NameSource: core.NameUser})
	l.agents = []claudecli.Agent{agent("s1", "aaaaaaaa", "background", "idle", 11), agent("s2", "bbbbbbbb", "background", "idle", 12)}
	if res := once(t, r); res.Updated != 2 {
		t.Fatalf("updated %d", res.Updated)
	}
	got := get(t, st, s.ID)
	if got.Kind != core.KindBackground || got.External != nil || got.BgID != "aaaaaaaa" || got.Pid != 11 || got.Name != "claude-name-aaaaaaaa" || got.Cwd != "/w/aaaaaaaa" {
		t.Errorf("corrected session: %+v", got)
	}
	if got := get(t, st, u.ID); got.Name != "mine" {
		t.Errorf("user name overwritten: %q", got.Name)
	}

	// Claude says interactive: external.
	l.agents = []claudecli.Agent{agent("s1", "", "interactive", "idle", 11)}
	once(t, r)
	if got := get(t, st, s.ID); got.Kind != core.KindExternal {
		t.Errorf("interactive not external: %+v", got)
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

func TestUnlistedSessions(t *testing.T) {
	r, st, _, c := setup(t)
	fresh := create(t, st, &core.Session{ClaudeSessionID: "new", Status: core.StatusStarting, CreatedAt: c.t})
	freshHook := create(t, st, &core.Session{ClaudeSessionID: "hook", Status: core.StatusDone, CreatedAt: c.t}) // first seen through hooks
	gone := create(t, st, &core.Session{ClaudeSessionID: "gone", Status: core.StatusDone, Pid: 3, CreatedAt: c.t.Add(-time.Hour)})
	extAlive := create(t, st, &core.Session{ClaudeSessionID: "ea", Kind: core.KindExternal, Status: core.StatusBusy, Pid: 100})
	extDead := create(t, st, &core.Session{ClaudeSessionID: "ed", Kind: core.KindExternal, Status: core.StatusDone, Pid: 200})
	paused := create(t, st, &core.Session{ClaudeSessionID: "p", Status: core.StatusPaused})
	r.Alive = func(pid int) bool { return pid == 100 }

	once(t, r)
	for id, want := range map[string]core.Status{fresh.ID: core.StatusStarting, freshHook.ID: core.StatusDone, gone.ID: core.StatusPaused,
		extAlive.ID: core.StatusBusy, extDead.ID: core.StatusPaused, paused.ID: core.StatusPaused} {
		if got := get(t, st, id); got.Status != want {
			t.Errorf("%s: %s, want %s", got.ClaudeSessionID, got.Status, want)
		}
	}
	if got := get(t, st, extDead.ID); got.Pid != 0 {
		t.Errorf("dead external pid not cleared: %d", got.Pid)
	}
	c.advance(time.Minute)
	once(t, r)
	for _, s := range []*core.Session{fresh, freshHook} {
		if got := get(t, st, s.ID); got.Status != core.StatusPaused {
			t.Errorf("%s never listed: %s, want paused after startup grace", got.ClaudeSessionID, got.Status)
		}
	}
}

func TestArchivedSessionsAreNotRediscovered(t *testing.T) {
	r, st, l, _ := setup(t)
	a := create(t, st, &core.Session{ClaudeSessionID: "s1", Status: core.StatusArchived, ArchivedAt: time.Now()})
	l.agents = []claudecli.Agent{agent("s1", "aaaaaaaa", "background", "idle", 9)}
	if res := once(t, r); res.Discovered != 0 {
		t.Errorf("archived session rediscovered: %+v", res)
	}
	if all, _ := st.Sessions(ctx, true); len(all) != 1 || all[0].ID != a.ID {
		t.Errorf("sessions: %+v", all)
	}
}

func TestListerError(t *testing.T) {
	r, _, l, _ := setup(t)
	l.err = errors.New("claude not found")
	if _, err := r.Once(ctx); err == nil {
		t.Error("expected error")
	}
}

func TestPidAlive(t *testing.T) {
	if !pidAlive(os.Getpid()) {
		t.Error("own pid should be alive")
	}
	if pidAlive(999999) {
		t.Error("pid 999999 should not be alive")
	}
}
