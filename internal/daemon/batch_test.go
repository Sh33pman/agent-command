package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/catalog"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/errcode"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/hostapi"
	"github.com/brettmostert/agent-command/internal/remote/wire"
	"github.com/brettmostert/agent-command/internal/store"
)

func codes(r ipc.Results) string {
	var out []string
	for _, x := range r.Results {
		if x.OK {
			out = append(out, "ok")
		} else {
			out = append(out, x.Code)
		}
	}
	return strings.Join(out, ",")
}

// Archiving several sessions: each that's there archives, and one that isn't
// says so. (A batch across a host's sessions: TestSessionsFromARemoteHost.)
func TestManyIdsGetAResultEach(t *testing.T) {
	old := remoteCallTimeout
	remoteCallTimeout = time.Second
	t.Cleanup(func() { remoteCallTimeout = old })
	p := settingsPaths(t)
	must(t, os.MkdirAll(p.StateDir, 0o700))
	st, err := store.Open(context.Background(), p.DB())
	must(t, err)
	ctx := context.Background()
	var ids []string
	for _, n := range []string{"a", "b", "c"} {
		s := &core.Session{Name: n, Cwd: "/w/" + n, Status: core.StatusDone, Harness: core.HarnessTerminal, Kind: core.KindHosted}
		must(t, st.CreateSession(ctx, s))
		ids = append(ids, s.ID)
	}
	must(t, st.SetAppState(ctx, movedKey, "done"))
	must(t, st.Close())

	start(t, p, withConfig(p, func(c *Config) { c.Stopper = &stops{} }))
	c := dial(t, p)
	began := time.Now()
	var res ipc.Results
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutArchive, Targets: ipc.Here(append(ids, "nope", ids[0])...)}, &res))
	if got := codes(res); got != "ok,ok,ok,not_found" {
		t.Fatalf("results = %s: %+v", got, res)
	}
	if took := time.Since(began); took > 2500*time.Millisecond {
		t.Errorf("waited for the host more than once: %v", took)
	}
	archived := 0
	for _, s := range snapshot(t, c).Sessions {
		if s.Status == core.StatusArchived {
			archived++
		}
	}
	if archived != 3 {
		t.Errorf("archived %d", archived)
	}

	// Unarchiving terminals isn't possible: each says so.
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutUnarchive, Targets: ipc.Here(ids[:2]...)}, &res))
	if codes(res) != "invalid,invalid" {
		t.Errorf("unarchive = %+v", res)
	}
}

func TestRenameManyIsNumbered(t *testing.T) {
	p := settingsPaths(t)
	start(t, p, withConfig(p))
	c := dial(t, p)
	var ids []string
	for _, cid := range []string{"r1", "r2", "r3"} {
		must(t, c.Call(ipc.OpEvent, hookEvent("SessionStart", `{"session_id":"`+cid+`","source":"startup","cwd":"/w"}`, time.Now(), ipc.HookContext{}), nil))
		ids = append(ids, byClaude(snapshot(t, c), cid).ID)
	}
	var res ipc.Results
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutRename, Targets: ipc.Here(ids...), Text: " api "}, &res))
	if codes(res) != "ok,ok,ok" {
		t.Fatalf("rename = %+v", res)
	}
	names := map[string]string{}
	for _, s := range snapshot(t, c).Sessions {
		names[s.ID] = s.Name
	}
	if names[ids[0]] != "api - 1" || names[ids[1]] != "api - 2" || names[ids[2]] != "api - 3" {
		t.Errorf("names = %v", names)
	}
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutRename, Targets: ipc.Here(ids[:1]...), Text: "solo"}, &res))
	for _, s := range snapshot(t, c).Sessions {
		if s.ID == ids[0] && s.Name != "solo" {
			t.Errorf("one keeps the name as given: %q", s.Name)
		}
	}
	if err := c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutRename, Targets: ipc.Here(ids...), Text: "  "}, nil); err == nil {
		t.Error("an empty name")
	}
	// Remote on, and moving, per id.
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutRemote, Targets: ipc.Here(ids...), Flag: true}, &res))
	if codes(res) != "ok,ok,ok" {
		t.Errorf("remote = %+v", res)
	}
	var g ipc.Group
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutGroupCreate, Text: "G"}, &g))
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutMove, Targets: ipc.Here(ids[0], "nope"), GroupID: g.ID}, &res))
	if codes(res) != "ok,not_found" {
		t.Errorf("move = %+v", res)
	}
	if core.RenameNames("x", 0) != nil {
		t.Error("none")
	}
}

// A session's screen as text: local sessions, all of it or its last lines;
// one on a host or one that's ended says so, with a code.
func TestSessionScreen(t *testing.T) {
	p := settingsPaths(t)
	must(t, os.MkdirAll(p.StateDir, 0o700))
	st, err := store.Open(context.Background(), p.DB())
	must(t, err)
	ctx := context.Background()
	local := &core.Session{Name: "api", Cwd: "/w", Status: core.StatusDone, Harness: core.HarnessTerminal, Kind: core.KindHosted}
	ended := &core.Session{Name: "old", Cwd: "/w", Status: core.StatusDone, Harness: core.HarnessTerminal, Kind: core.KindHosted}
	far := &core.Session{Name: "far", Cwd: "/r", Status: core.StatusDone, Harness: core.HarnessClaude, Kind: core.KindHosted, Host: "studio", RemoteID: "r1"}
	for _, s := range []*core.Session{local, ended, far} {
		must(t, st.CreateSession(ctx, s))
	}
	must(t, st.SetAppState(ctx, movedKey, "done"))
	must(t, st.Close())
	start(t, p, withConfig(p, func(c *Config) {
		c.HostScreen = func(id string) (string, bool) {
			if id == local.ID {
				return "$ make\r\n\x1b[32mok\x1b[0m\n$ ", true
			}
			return "", false
		}
	}))
	c := dial(t, p)
	var sc ipc.Screen
	must(t, c.Call(ipc.OpSessionScreen, ipc.ScreenRequest{ID: local.ID}, &sc))
	if sc.Text != "$ make\nok\n$ " || sc.Lines != 3 || sc.Total != 3 {
		t.Errorf("screen = %+v", sc)
	}
	must(t, c.Call(ipc.OpSessionScreen, ipc.ScreenRequest{ID: local.ID, Lines: 1}, &sc))
	if sc.Text != "$ " || sc.Lines != 1 || sc.Total != 3 {
		t.Errorf("last line = %+v", sc)
	}
	for id, code := range map[string]string{far.ID: ipc.CodeNotSupported, ended.ID: ipc.CodeNotRunning, "nope": ipc.CodeNotFound} {
		err := c.Call(ipc.OpSessionScreen, ipc.ScreenRequest{ID: id}, nil)
		var re *ipc.RemoteError
		if !errors.As(err, &re) || re.Code != code {
			t.Errorf("%s: %v (want code %s)", id, err, code)
		}
	}
	if err := c.Call(ipc.OpSessionScreen, ipc.ScreenRequest{ID: local.ID, Lines: -1}, nil); err == nil {
		t.Error("lines -1")
	}
	if err := c.Call(ipc.OpSessionScreen, "nope", nil); err == nil {
		t.Error("took a request that isn't one")
	}
}

// A paired device runs the daemon's ops as its socket would (Direct Connect):
// a change bumps the revision, an error keeps its code, and sessions and
// groups go as their wire types.
func TestHostBackendRunsOps(t *testing.T) {
	d, _, _, _ := jobDaemon(t)
	b := hostBackend{d}
	// A pairing that may act: what it may run is the dispatcher's to say.
	ctx := hostapi.WithCaller(context.Background(), catalog.Caller{Scope: catalog.ScopeAct, Name: "laptop"})
	raw, err := b.Call(ctx, ipc.OpNew, json.RawMessage(`{"cwd":"`+t.TempDir()+`","harness":"terminal","name":"api"}`))
	if err != nil {
		t.Fatal(err)
	}
	var s ipc.Session
	must(t, json.Unmarshal(raw, &s))
	if s.Name != "api" || s.Parent == "" {
		t.Errorf("new = %s", raw)
	}
	rev, _ := d.changes.current()
	if _, err := b.Call(ctx, ipc.OpMutate, json.RawMessage(`{"kind":"session.rename","targets":[{"id":"`+s.ID+`"}],"text":"x"}`)); err != nil {
		t.Fatal(err)
	}
	if now, _ := d.changes.current(); now == rev {
		t.Error("a change didn't bump the revision")
	}
	if raw, err := b.Call(ctx, ipc.OpPing, nil); err != nil || raw != nil {
		t.Errorf("ping = %s %v", raw, err)
	}
	_, err = b.Call(ctx, ipc.OpSessionScreen, json.RawMessage(`{"id":"nope"}`))
	if !wire.IsCode(err, ipc.CodeNotFound) {
		t.Errorf("screen of nothing: %v", err)
	}
	// The one dispatcher's rule, whatever the transport: a pairing that may
	// only read is refused a change, with permission_denied; an op for the
	// host's own machine is refused to any pairing, with forbidden.
	reader := hostapi.WithCaller(context.Background(), catalog.Caller{Scope: catalog.ScopeRead, Name: "phone"})
	_, err = b.Call(reader, ipc.OpMutate, json.RawMessage(`{"kind":"session.rename","targets":[{"id":"`+s.ID+`"}],"text":"y"}`))
	if !wire.IsCode(err, errcode.PermissionDenied) || !strings.Contains(err.Error(), "acmd remote scope phone act") {
		t.Errorf("a reader renamed: %v", err)
	}
	if err := b.Allow(reader, ipc.OpSnapshot); err != nil {
		t.Errorf("a reader can't read: %v", err)
	}
	if err := b.Allow(reader, ipc.OpConfigSet); resultOf("", err).Code != errcode.PermissionDenied {
		t.Errorf("a reader set config: %v", err)
	}
	if err := b.Allow(ctx, ipc.OpShutdown); resultOf("", err).Code != errcode.Forbidden {
		t.Errorf("a pairing stopped the daemon: %v", err)
	}
	if err := b.Allow(ctx, "no.such.op"); resultOf("", err).Code != errcode.UnknownOp {
		t.Errorf("an unknown op: %v", err)
	}
	// On this machine's socket the caller is the user: everything.
	here := context.WithValue(context.Background(), callerKey{}, &caller{local: true})
	if err := d.allow(here, ipc.OpShutdown); err != nil {
		t.Errorf("this machine: %v", err)
	}
	if _, err := d.dispatch(here, ipc.Request{Op: ipc.OpMutate, Data: json.RawMessage(`{}`)}); resultOf("", err).Code != errcode.BadRequest {
		t.Errorf("a mutation with no kind: %v", err)
	}
}
