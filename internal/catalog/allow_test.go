package catalog

import (
	"errors"
	"testing"

	"github.com/brettmostert/agent-command/internal/errcode"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// The one rule every transport checks ops by (design 6.12): everything on
// this machine; from another, never the host's own machine's ops or its own
// hosts', and the rest by the pairing's scope.
func TestAllow(t *testing.T) {
	code := func(err error) string {
		var d *DeniedError
		if errors.As(err, &d) {
			return d.Code()
		}
		if err != nil {
			return "?"
		}
		return ""
	}
	local := Caller{Local: true}
	admin := Caller{Scope: ScopeAdmin, Name: "laptop"}
	read := Caller{Scope: ScopeRead, Name: "phone"}
	for _, op := range Local.Ops() {
		if err := local.Allow(op); err != nil {
			t.Errorf("this machine refused %s: %v", op.Name, err)
		}
	}
	for name, want := range map[string]string{
		ipc.OpSnapshot: "", ipc.OpSessionScreen: "", ipc.MutRename: "", ipc.OpNew: "", ipc.OpJobSave: "",
		ipc.OpInboxAnswer: "", ipc.OpConfigSet: "", ipc.MutPurge: "", ipc.OpRemoteScope: "", ipc.OpSubscribe: "",
		ipc.OpSessionAttach: "",
		ipc.OpEvent:         errcode.Forbidden, ipc.OpShutdown: errcode.Forbidden, ipc.OpWakeSet: errcode.Forbidden,
		ipc.OpSpeechSay: errcode.Forbidden, ipc.OpRemoteHosts: errcode.Forbidden, ipc.OpRemoteCall: errcode.Forbidden,
		ipc.OpRemoteNew: errcode.Forbidden,
	} {
		op, ok := Local.Lookup(name)
		if !ok {
			t.Fatalf("no op %s", name)
		}
		if got := code(admin.Allow(op)); got != want {
			t.Errorf("an admin pairing, %s: %q, want %q", name, got, want)
		}
	}
	for name, want := range map[string]string{
		ipc.OpSnapshot: "", ipc.OpSubscribe: "", ipc.OpSessionScreen: "",
		ipc.MutRename: errcode.PermissionDenied, ipc.OpSessionAttach: errcode.PermissionDenied,
		ipc.OpConfigSet: errcode.PermissionDenied, ipc.MutPurge: errcode.PermissionDenied,
	} {
		op, _ := Local.Lookup(name)
		err := read.Allow(op)
		if got := code(err); got != want {
			t.Errorf("a read pairing, %s: %q, want %q", name, got, want)
		}
		if want == errcode.PermissionDenied && err.Error() == "" {
			t.Error("no message")
		}
	}
	if (Caller{}).Allow(Op{Name: "x", Scope: ScopeRead}) == nil {
		t.Error("a caller with no scope ran a read op")
	}
}
