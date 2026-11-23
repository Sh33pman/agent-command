package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/ipc"
)

// helloAs connects as a client of kind at version, as an app does: it speaks
// to a daemon of another version (the CLI would replace it; an app doesn't).
func helloAs(t *testing.T, p interface{ Socket() string }, id, kind, version string) *ipc.Conn {
	t.Helper()
	c, err := ipc.Dial(context.Background(), p.Socket(), ipc.Hello{Proto: ipc.Proto, ProtoMin: ipc.ProtoMin, ProtoMax: ipc.Proto,
		Version: version, Client: &ipc.ClientHello{ID: id, Kind: kind, Caps: []string{ipc.CapNotify}}})
	var mismatch *ipc.ErrVersionMismatch
	if err != nil && !errors.As(err, &mismatch) {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func healthOf(t *testing.T, c *ipc.Conn, id string) *ipc.Health {
	t.Helper()
	var snap ipc.Snapshot
	must(t, c.Call(ipc.OpSnapshot, nil, &snap))
	for i := range snap.Health {
		if snap.Health[i].ID == id {
			return &snap.Health[i]
		}
	}
	return nil
}

// acmd older than the app using it: the app and acmd upgrade separately, and
// the app may not show everything. The health list says so.
func TestWarnsWhenAcmdIsOlderThanTheApp(t *testing.T) {
	p := testPaths(t)
	start(t, p, func(c *Config) { c.Version = "0.87.0" })

	dev := helloAs(t, p, "mac-dev", ipc.KindMac, "app") // a dev build names no version
	if h := healthOf(t, dev, ipc.HealthAcmdOlder); h != nil {
		t.Errorf("a dev app: %+v", h)
	}
	older := helloAs(t, p, "mac-old", ipc.KindMac, "0.86.0")
	if h := healthOf(t, older, ipc.HealthAcmdOlder); h != nil {
		t.Errorf("an older app: %+v", h)
	}
	term := helloAs(t, p, "terminal-1", ipc.KindTerminal, "0.91.0") // not an app
	if h := healthOf(t, term, ipc.HealthAcmdOlder); h != nil {
		t.Errorf("a newer terminal client: %+v", h)
	}

	app := helloAs(t, p, "mac-1", ipc.KindMac, "0.91.0")
	h := healthOf(t, app, ipc.HealthAcmdOlder)
	if h == nil || h.Severity != ipc.HealthWarning || !strings.Contains(h.Detail, "acmd 0.87.0 is older than the app (0.91.0)") ||
		h.Fix == nil || !strings.Contains(h.Fix.Step, "brew upgrade") {
		t.Fatalf("health = %+v", h)
	}
	// Every client sees it: it's the machine's.
	if healthOf(t, dev, ipc.HealthAcmdOlder) == nil {
		t.Error("another client doesn't see it")
	}
}

func TestNoWarningWhenAcmdIsntAReleasedVersion(t *testing.T) {
	p := testPaths(t)
	start(t, p) // testVersion: a dev build
	app := helloAs(t, p, "mac-1", ipc.KindMac, "0.91.0")
	if h := healthOf(t, app, ipc.HealthAcmdOlder); h != nil {
		t.Errorf("a dev acmd: %+v", h)
	}
	if helloVersion([]byte("nope")) != "" {
		t.Error("a version from a hello that isn't one")
	}
	(&Daemon{}).clientVersion(nil, "1.0.0") // no registry, no client: nothing to do
}
