package doctor

import (
	"os"
	"strings"
	"testing"
)

func TestRemoteSessionsWithHostingOff(t *testing.T) {
	f := newFake(t)
	f.daemon.Status.RemoteSessions = 2
	r, out := f.run()
	if r.Count(Warn) != 1 || !strings.Contains(out, "Remote sessions\n  ! 2 sessions marked Remote, but Hosting is off: no client can reach them\n"+
		"      fix: set enabled = true under [remote.host] in ~/config/config.toml") {
		t.Errorf("no warning about Hosting:\n%s", out)
	}

	b, err := os.ReadFile(f.paths().Config())
	must(t, err)
	f.write(f.paths().Config(), string(b)+"\n[remote.host]\nenabled = true\n", 0o600)
	f.daemon.Status.RemoteSessions = 1
	r, out = f.run()
	if r.Count(Warn) != 0 || !strings.Contains(out, "✓ 1 Remote session, Hosting on at 127.0.0.1:7433") {
		t.Errorf("with Hosting on:\n%s", out)
	}

	f.daemon.Status.RemoteSessions = 0
	if _, out := f.run(); strings.Contains(out, "Remote sessions") {
		t.Errorf("no Remote sessions, but the section shows:\n%s", out)
	}
	f.write(f.paths().Config(), "[remote\n", 0o600)
	f.daemon.Status.RemoteSessions = 1
	if _, out := f.run(); strings.Contains(out, "Remote sessions") {
		t.Errorf("a broken config is reported once, by the acmd section:\n%s", out)
	}
}
