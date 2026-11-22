package hostapi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/api"
	"github.com/brettmostert/agent-command/internal/remote/wire"
)

func callReq(t *testing.T, op string, data any) api.CallRequest {
	t.Helper()
	r := api.CallRequest{Op: op}
	if data != nil {
		b, err := json.Marshal(data)
		must(t, err)
		r.Data = b
	}
	return r
}

// A paired device runs the host daemon's own ops (Direct Connect), except ones
// for the host's own machine, admin ones, streams and the host's remote.* ops.
func TestCallRunsTheHostsOps(t *testing.T) {
	f := newFixture(t)
	f.b.replies = map[string]json.RawMessage{ipc.OpSnapshot: json.RawMessage(`{"sessions":[{"id":"l1"}]}`)}

	var snap ipc.Snapshot
	must(t, f.call(api.OpCall, callReq(t, ipc.OpSnapshot, nil), &snap))
	if len(snap.Sessions) != 1 || snap.Sessions[0].ID != "l1" {
		t.Errorf("snapshot = %+v", snap)
	}
	// A change through mutate is checked by its kind.
	must(t, f.call(api.OpCall, callReq(t, ipc.OpMutate, ipc.Mutation{Kind: ipc.MutRename, Targets: ipc.Here("l1"), Text: "x"}), nil))
	if last := f.b.calls[len(f.b.calls)-1]; last.Op != ipc.OpMutate || !json.Valid(last.Data) || last.RequestID != "" {
		t.Errorf("mutate went as %+v", last)
	}
	// A request id goes along to the daemon, which runs a retry once.
	withID := callReq(t, ipc.OpMutate, ipc.Mutation{Kind: ipc.MutGroupCreate, Text: "G"})
	withID.RequestID = "q1"
	must(t, f.call(api.OpCall, withID, nil))
	if last := f.b.calls[len(f.b.calls)-1]; last.RequestID != "q1" {
		t.Errorf("the request id didn't go along: %+v", last)
	}
	// Admin ops need admin: this pairing has it; one that may only act doesn't.
	must(t, f.call(api.OpCall, callReq(t, ipc.OpInit, nil), nil))
	_, err := f.h.SetScope("laptop", config.ScopeAct)
	must(t, err)
	for _, r := range []api.CallRequest{
		callReq(t, ipc.OpMutate, ipc.Mutation{Kind: ipc.MutPurge, Targets: ipc.Here("l1")}),
		callReq(t, ipc.OpInit, nil),
		callReq(t, ipc.OpConfigSet, ipc.ConfigSet{Values: map[string]any{"notify.sound": ""}}),
	} {
		wantCode(t, r.Op+" with act", f.call(api.OpCall, r, nil), api.CodePermission)
	}
	_, err = f.h.SetScope("laptop", config.ScopeRead)
	must(t, err)
	wantCode(t, "rename with read", f.call(api.OpCall, callReq(t, ipc.OpMutate, ipc.Mutation{Kind: ipc.MutRename, Targets: ipc.Here("l1"), Text: "x"}), nil),
		api.CodePermission)
	must(t, f.call(api.OpCall, callReq(t, ipc.OpSnapshot, nil), &snap)) // reading still works
	wantCode(t, "new with read", f.call(api.OpSessionNew, api.NewSession{Dir: "~"}, nil), api.CodePermission)
	wantCode(t, "config.set with read", f.call(api.OpConfigSet, ipc.ConfigSet{}, nil), api.CodePermission)
	_, err = f.h.SetScope("laptop", config.ScopeAdmin)
	must(t, err)
	for _, r := range []api.CallRequest{
		callReq(t, ipc.OpSpeechSay, map[string]string{}), // the host's own machine
		callReq(t, ipc.OpRemoteHosts, nil),               // the host's own hosts
		callReq(t, ipc.OpRemoteCall, ipc.RemoteCall{}),   // nor through them
		callReq(t, ipc.OpHello, ipc.Hello{}),
	} {
		wantCode(t, r.Op+" "+string(r.Data), f.call(api.OpCall, r, nil), api.CodeForbidden)
	}
	wantCode(t, "unknown", f.call(api.OpCall, callReq(t, "nope", nil), nil), wire.CodeUnknownOp)
	wantCode(t, "bad mutate", f.call(api.OpCall, api.CallRequest{Op: ipc.OpMutate, Data: json.RawMessage(`"x"`)}, nil), wire.CodeBadRequest)
	f.b.callErr = wire.Errorf("not_supported", "can't")
	wantCode(t, "the daemon's code", f.call(api.OpCall, callReq(t, ipc.OpSessionScreen, ipc.ScreenRequest{ID: "l1"}), nil), "not_supported")
	f.b.callErr = nil
	wantCode(t, "bad call", f.call(api.OpCall, "x", nil), wire.CodeBadRequest)
}

// config.get and config.set are the host's settings, as on their own: not its
// device ones.
func TestCallSettingsAreTheHosts(t *testing.T) {
	f := newFixture(t)
	var st ipc.ConfigState
	must(t, f.call(api.OpCall, callReq(t, ipc.OpConfigGet, nil), &st))
	if _, ok := st.Values["editor"]; ok {
		t.Errorf("a device setting came: %v", st.Values)
	}
	must(t, f.call(api.OpCall, callReq(t, ipc.OpConfigSet, ipc.ConfigSet{Values: map[string]any{"default_harness": "antigravity"}}), &st))
	if st.Values["default_harness"] != "antigravity" {
		t.Errorf("set: %v", st.Values)
	}
	wantCode(t, "device", f.call(api.OpCall, callReq(t, ipc.OpConfigSet, ipc.ConfigSet{Values: map[string]any{"editor": "vim"}}), nil),
		wire.CodeBadRequest)
	if len(f.b.calls) != 0 {
		t.Errorf("settings went to the daemon's ops: %+v", f.b.calls)
	}
}

// A new session's folder is on the host, from its home; the harness is its
// default, and must be installed.
func TestCallStartsSessionsInTheHostsFolders(t *testing.T) {
	f := newFixture(t)
	must(t, os.Mkdir(filepath.Join(f.home, "api"), 0o750))
	f.b.installed, f.b.def = []string{"claude", "terminal"}, "terminal"
	must(t, f.call(api.OpCall, callReq(t, ipc.OpNew, ipc.NewRequest{Cwd: "~/api", Name: "x"}), nil))
	var got ipc.NewRequest
	must(t, json.Unmarshal(f.b.calls[0].Data, &got))
	if got.Cwd != filepath.Join(f.home, "api") || got.Harness != "terminal" || got.Name != "x" {
		t.Errorf("new = %+v", got)
	}
	wantCode(t, "no folder", f.call(api.OpCall, callReq(t, ipc.OpNew, ipc.NewRequest{Cwd: "~/nope"}), nil), wire.CodeNotFound)
	wantCode(t, "not installed", f.call(api.OpCall, callReq(t, ipc.OpNew, ipc.NewRequest{Cwd: "~", Harness: "antigravity"}), nil),
		wire.CodeNotFound)
	wantCode(t, "bad new", f.call(api.OpCall, api.CallRequest{Op: ipc.OpNew, Data: json.RawMessage(`[]`)}, nil), wire.CodeBadRequest)
}

// A paired device follows the host daemon's event stream, as on its socket.
func TestSubscribeStreamsTheHost(t *testing.T) {
	f := newFixture(t)
	s, err := f.open(api.OpSubscribe, ipc.SubscribeRequest{Types: []string{ipc.EntitySession}})
	must(t, err)
	defer s.Close()
	var ev ipc.Event
	must(t, s.Receive(&ev))
	if ev.Type != ipc.EventSnapshot || ev.Snapshot == nil {
		t.Errorf("first = %+v", ev)
	}
	if _, err := f.open(api.OpSubscribe, "x"); err == nil {
		t.Error("a request that isn't one")
	}
}

// A stream needs its scope too: read follows, nothing less.
func TestStreamsNeedAScope(t *testing.T) {
	f := newFixture(t)
	_, err := f.h.SetScope("laptop", "none")
	if err == nil {
		t.Fatal("none is a scope")
	}
	if _, ok := PeerOf(context.Background()); ok {
		t.Error("a peer from nowhere")
	}
	_, err = f.h.Revoke("laptop") // its connection goes: a new one isn't let in
	must(t, err)
	if s, err := f.open(api.OpSubscribe, ipc.SubscribeRequest{}); err == nil {
		var ev ipc.Event
		if s.Receive(&ev) == nil {
			t.Errorf("a revoked client streamed: %+v", ev)
		}
	}
}
