package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"
)

// A daemon the service started follows upgrades: when the binary the service
// runs (/opt/homebrew/bin/acmd, the systemd unit's, Scoop's current\acmd.exe)
// becomes another build, it exits, and the service starts the new one.
// Sessions keep running. Without this, the old daemon would run on until the
// next acmd command replaced it, and the app never does.

// upgradeEvery is how often the service's binary is looked at.
var upgradeEvery = 30 * time.Second

// binaryWatch notices the service's binary becoming another build of acmd.
type binaryWatch struct {
	path    string
	start   os.FileInfo // the binary when the daemon started
	running string      // this daemon's version
	stat    func(string) (os.FileInfo, error)
	// versionOf is the version a binary names (acmd version).
	versionOf func(ctx context.Context, path string) (string, error)
}

// newBinaryWatch watches path, as it is now, for a daemon of version running.
func newBinaryWatch(path, running string) (*binaryWatch, error) {
	w := &binaryWatch{path: path, running: running, stat: os.Stat, versionOf: binaryVersion}
	st, err := w.stat(path)
	if err != nil {
		return nil, err
	}
	w.start = st
	return w, nil
}

// upgraded says the version the binary now names, when it's another build
// of another version. A binary that's half written, or doesn't run, isn't
// one yet; the same version reinstalled is the same.
func (w *binaryWatch) upgraded(ctx context.Context) (string, bool) {
	st, err := w.stat(w.path)
	if err != nil || os.SameFile(st, w.start) {
		return "", false
	}
	v, err := w.versionOf(ctx, w.path)
	if err != nil || v == "" {
		return "", false
	}
	if v == w.running {
		w.start = st
		return "", false
	}
	return v, true
}

// follow looks at the binary every interval until ctx ends, and calls
// onUpgrade once with the new version when it's another one.
func (w *binaryWatch) follow(ctx context.Context, every time.Duration, onUpgrade func(version string)) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if v, ok := w.upgraded(ctx); ok {
				onUpgrade(v)
				return
			}
		}
	}
}

// binaryVersion runs `path version` and reads its version.
func binaryVersion(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").Output() // #nosec G204 -- the binary the service runs
	if err != nil {
		return "", err
	}
	// "agent-command 0.91.0 (commit …)"
	f := strings.Fields(string(out))
	if len(f) < 2 || f[0] != "agent-command" {
		return "", errors.New("not acmd's version line")
	}
	return f[1], nil
}
