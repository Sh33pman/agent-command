package daemon

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
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

// Archiving five sessions, two of them on a host that can't be reached:
// three archive, the host's two say why, and the host is waited for once.
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
	for _, n := range []string{"far1", "far2"} {
		s := &core.Session{Name: n, Cwd: "/r", Status: core.StatusDone, Harness: core.HarnessClaude, Kind: core.KindHosted,
			Host: "far", RemoteID: "r-" + n}
		must(t, st.CreateSession(ctx, s))
		ids = append(ids, s.ID)
	}
	must(t, st.SaveKnownHost(ctx, "", config.KnownHost{Name: "far", Address: "127.0.0.1:1",
		Fingerprint: "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}))
	must(t, st.SetAppState(ctx, movedKey, "done"))
	must(t, st.Close())

	start(t, p, withConfig(p, func(c *Config) { c.Stopper = &stops{} }))
	c := dial(t, p)
	began := time.Now()
	var res ipc.Results
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutArchive, IDs: append(ids, "nope", ids[0])}, &res))
	if got := codes(res); got != "ok,ok,ok,host_unreachable,host_unreachable,not_found" {
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
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutUnarchive, IDs: ids[:2]}, &res))
	if codes(res) != "invalid,invalid" {
		t.Errorf("unarchive = %+v", res)
	}
	// Removing added sessions: only a host's.
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutRemoveAdded, IDs: []string{ids[3], ids[0]}}, &res))
	if codes(res) != "ok,invalid" {
		t.Errorf("remove added = %+v", res)
	}
	if err := c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutRemoveAdded, ID: ids[4]}, nil); err == nil {
		t.Error("remove_added with one id, not ids")
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
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutRename, IDs: ids, Text: " api "}, &res))
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
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutRename, IDs: ids[:1], Text: "solo"}, &res))
	for _, s := range snapshot(t, c).Sessions {
		if s.ID == ids[0] && s.Name != "solo" {
			t.Errorf("one keeps the name as given: %q", s.Name)
		}
	}
	if err := c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutRename, IDs: ids, Text: "  "}, nil); err == nil {
		t.Error("an empty name")
	}
	// Remote on, and moving, per id.
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutRemote, IDs: ids, Flag: true}, &res))
	if codes(res) != "ok,ok,ok" {
		t.Errorf("remote = %+v", res)
	}
	var g core.Group
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutGroupCreate, Text: "G"}, &g))
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutMove, IDs: []string{ids[0], "nope"}, GroupID: g.ID}, &res))
	if codes(res) != "ok,not_found" {
		t.Errorf("move = %+v", res)
	}
	if core.RenameNames("x", 0) != nil {
		t.Error("none")
	}
}
