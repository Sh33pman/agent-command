package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/errcode"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// acmd doctor's checks and opening the editor are the daemon's ops, so the
// app runs acmd only for waking the Mac (design 6.4).
func TestDoctorAndTheEditorAreOps(t *testing.T) {
	d, _, _, _ := jobDaemon(t)
	ctx := context.Background()
	call := func(op string, in any) (any, error) {
		data, _ := json.Marshal(in)
		return d.handle(ctx, ipc.Request{Op: op, Data: data})
	}
	if _, err := call(ipc.OpDoctor, nil); resultOf("", err).Code != errcode.NotSupported {
		t.Errorf("no doctor: %v", err)
	}
	if _, err := call(ipc.OpEditorOpen, ipc.EditorOpen{Path: "/w"}); resultOf("", err).Code != errcode.NotSupported {
		t.Errorf("no editor: %v", err)
	}
	d.cfg.Doctor = func(context.Context) ipc.DoctorReport {
		return ipc.DoctorReport{Checks: []ipc.DoctorCheck{{Section: "acmd", Status: ipc.DoctorOK, Text: "fine"}}}
	}
	var opened []ipc.EditorOpen
	d.cfg.OpenEditor = func(_ context.Context, r ipc.EditorOpen) error {
		if r.Path == "/broken" {
			return errors.New("no editor chosen yet")
		}
		opened = append(opened, r)
		return nil
	}
	out, err := call(ipc.OpDoctor, nil)
	if r, ok := out.(ipc.DoctorReport); err != nil || !ok || len(r.Checks) != 1 || r.Checks[0].Status != ipc.DoctorOK {
		t.Errorf("doctor = %+v %v", out, err)
	}
	s := &core.Session{Name: "api", Cwd: "/w/api", Status: core.StatusDone, Harness: core.HarnessTerminal, Kind: core.KindHosted}
	must(t, d.store.CreateSession(ctx, s))
	empty := &core.Session{Name: "new", Status: core.StatusDone, Harness: core.HarnessTerminal, Kind: core.KindHosted}
	must(t, d.store.CreateSession(ctx, empty))
	_, err = call(ipc.OpEditorOpen, ipc.EditorOpen{ID: s.ID})
	must(t, err)
	_, err = call(ipc.OpEditorOpen, ipc.EditorOpen{Path: "a.go", In: "/w", Line: 4, Column: 2})
	must(t, err)
	if len(opened) != 2 || opened[0].Path != "/w/api" || opened[1] != (ipc.EditorOpen{Path: "a.go", In: "/w", Line: 4, Column: 2}) {
		t.Errorf("opened %+v", opened)
	}
	for in, code := range map[ipc.EditorOpen]string{
		{ID: "nope"}: errcode.NotFound, {ID: empty.ID}: errcode.Invalid, {}: errcode.BadRequest,
		{Path: "/broken"}: errcode.Internal,
	} {
		if _, err := call(ipc.OpEditorOpen, in); resultOf("", err).Code != code {
			t.Errorf("%+v: %v, want %s", in, err, code)
		}
	}
	if _, err := d.handle(ctx, ipc.Request{Op: ipc.OpEditorOpen, Data: json.RawMessage(`"x"`)}); resultOf("", err).Code != errcode.BadRequest {
		t.Errorf("a request that isn't one: %v", err)
	}
}
