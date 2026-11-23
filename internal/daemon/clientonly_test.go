package daemon

import (
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/ipc"
)

// A client-only daemon (Windows) runs no sessions of its own: what needs them
// answers not_supported, the features say so, and Hosting never listens. Using
// a host's sessions still works.
func TestClientOnlyDaemon(t *testing.T) {
	p := testPaths(t)
	p.ConfigDir = filepath.Join(filepath.Dir(p.StateDir), "c")
	// Hosting turned on by hand stays off.
	must(t, writeSetting(p.Config(), "remote.host", "enabled", true))
	start(t, p, func(c *Config) { c.ClientOnly = true; c.ConfigFile = p.Config() })
	c := dial(t, p)

	for _, call := range []struct {
		op string
		in any
	}{
		{ipc.OpNew, ipc.NewRequest{Cwd: t.TempDir()}},
		{ipc.OpRemoteHosting, ipc.RemoteRef{Enabled: true}},
		{ipc.OpRemoteStatus, nil},
		{ipc.OpJobRuns, ipc.JobRunsRequest{}},
		{ipc.OpSpeechStatus, nil},
		{ipc.OpEditorOpen, ipc.EditorOpen{Path: t.TempDir()}},
		{ipc.OpFoldersRecent, nil},
	} {
		err := c.Call(call.op, call.in, nil)
		var re *ipc.RemoteError
		if !errors.As(err, &re) || re.Code != ipc.CodeNotSupported || !strings.Contains(re.Msg, "acmd remote add") {
			t.Errorf("%s: %v, want not_supported pointing at acmd remote add", call.op, err)
		}
	}

	var snap ipc.Snapshot
	must(t, c.Call(ipc.OpSnapshot, nil, &snap))
	if f := snap.Features; f == nil || f.Host.Sessions || f.Host.Hosting || f.Host.Wake || f.Device.Speech || f.Device.Editor || len(f.Host.HarnessKinds) != 0 {
		t.Errorf("features = %+v, want nothing but the OS", f)
	}
	if snap.Remote != nil {
		t.Errorf("snapshot's Hosting = %+v, want none", snap.Remote)
	}

	// What using a host needs.
	var hosts []ipc.RemoteHost
	must(t, c.Call(ipc.OpRemoteHosts, nil, &hosts))
	var id ipc.RemoteIdentity
	must(t, c.Call(ipc.OpRemoteIdentity, nil, &id))
	if id.Fingerprint == "" {
		t.Error("no identity to pair with")
	}
}

func TestADaemonThatRunsSessionsSaysSo(t *testing.T) {
	p := testPaths(t)
	start(t, p)
	var snap ipc.Snapshot
	must(t, dial(t, p).Call(ipc.OpSnapshot, nil, &snap))
	if snap.Features == nil || !snap.Features.Host.Sessions {
		t.Errorf("features = %+v, want sessions", snap.Features)
	}
}

// sessionsHere skips a test that runs sessions on this machine: their hosts
// need a pty, which Windows has none of here (no ConPTY host), and Windows'
// daemon is client-only. TestClientOnlyDaemon covers it there.
func sessionsHere(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("runs a session here; Windows is client-only")
	}
}
