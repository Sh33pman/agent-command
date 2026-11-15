// Package paths resolves where acmd keeps its state and runtime files.
// ACMD_STATE_DIR, ACMD_RUNTIME_DIR and ACMD_CONFIG_DIR
// override the defaults (tests, and running several isolated instances).
package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// maxSunPath is macOS's limit for unix socket paths (sun_path, incl. NUL).
const maxSunPath = 104

// Paths are acmd's file locations.
type Paths struct {
	StateDir   string // ~/.local/state/agent-command
	RuntimeDir string // macOS: $TMPDIR/agent-command-$UID. Linux: $XDG_RUNTIME_DIR/agent-command. Or StateDir/run
	ConfigDir  string // ~/.config/agent-command
}

// Default resolves the paths for the current user.
func Default() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	state := os.Getenv("ACMD_STATE_DIR")
	if state == "" {
		if xdg := os.Getenv("XDG_STATE_HOME"); runtime.GOOS == "linux" && xdg != "" {
			state = filepath.Join(xdg, "agent-command")
		} else {
			state = filepath.Join(home, ".local", "state", "agent-command")
		}
	}
	config := os.Getenv("ACMD_CONFIG_DIR")
	if config == "" {
		if xdg := os.Getenv("XDG_CONFIG_HOME"); runtime.GOOS == "linux" && xdg != "" {
			config = filepath.Join(xdg, "agent-command")
		} else {
			config = filepath.Join(home, ".config", "agent-command")
		}
	}
	runtimeDir := os.Getenv("ACMD_RUNTIME_DIR")
	if runtimeDir == "" {
		runtimeDir = defaultRuntimeDir(runtime.GOOS, os.Getenv("XDG_RUNTIME_DIR"), os.TempDir(), state, os.Getuid())
	}
	return Paths{StateDir: state, RuntimeDir: runtimeDir, ConfigDir: config}, nil
}

// defaultRuntimeDir is where the sockets go when ACMD_RUNTIME_DIR isn't set.
// It is never a folder in a shared place like /tmp, where another user could
// make it first and have acmd talk to their socket.
func defaultRuntimeDir(goos, xdgRuntime, tmp, state string, uid int) string {
	var dir string
	switch {
	case goos == "linux" && xdgRuntime != "":
		dir = filepath.Join(xdgRuntime, "agent-command")
	case goos == "linux":
		// No XDG_RUNTIME_DIR (cron, some SSH and container sessions). Linux's
		// temp folder is the shared /tmp, so the sockets go in the user's own
		// state folder.
		return filepath.Join(state, "run")
	default:
		// macOS's TMPDIR is the user's own.
		dir = filepath.Join(tmp, fmt.Sprintf("agent-command-%d", uid))
	}
	// A TMPDIR or XDG_RUNTIME_DIR too long for a socket also falls back to the
	// state folder.
	if len(filepath.Join(dir, "daemon.sock")) >= maxSunPath {
		return filepath.Join(state, "run")
	}
	return dir
}

// Ensure creates both folders, and makes sure each is the user's own and 0700.
// It refuses a folder that belongs to someone else. The runtime folder must
// also be a real folder, not a symlink: where it can be in a shared place like
// /tmp, another user could make it first, or point it somewhere else, and have
// acmd put its sockets there. The state folder may be a symlink, as it is in
// the user's home, where only they can make one.
func (p Paths) Ensure() error {
	if err := ensureDir(p.StateDir, false); err != nil {
		return err
	}
	return ensureDir(p.RuntimeDir, true)
}

func ensureDir(dir string, realFolder bool) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return ownDir(dir, os.Getuid(), realFolder)
}

// ownDir checks that dir is a folder that belongs to uid, and makes it 0700.
func ownDir(dir string, uid int, realFolder bool) error {
	if realFolder {
		lst, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		if lst.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink, not a folder: refusing to use it", dir)
		}
	}
	st, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("%s is not a folder", dir)
	}
	if owner, known := ownerUID(st); known && owner != uid {
		return fmt.Errorf("%s belongs to user %d, not %d: refusing to use it", dir, owner, uid)
	}
	if st.Mode().Perm() == 0o700 {
		return nil
	}
	// The mode is changed through the open folder, after checking it is the one
	// that was just looked at, so it can't be swapped for another in between.
	f, err := os.Open(dir) //nolint:gosec // G304: acmd's own folder, checked above
	if err != nil {
		return err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(st, opened) {
		return fmt.Errorf("%s changed while it was being checked: refusing to use it", dir)
	}
	// Folders need the execute bit; 0700 is owner-only.
	return f.Chmod(0o700)
}

// OldPopupLock is the lock of v1's popup, which no longer exists.
func (p Paths) OldPopupLock() string { return filepath.Join(p.RuntimeDir, "popup.lock") }

// RemoveRuntime removes acmd's files from the runtime folder, then the folder if
// that leaves it empty. Only the files acmd makes: ACMD_RUNTIME_DIR can point at a
// folder with other things in it. Run it once the daemon and hosts have stopped, as
// they're using these.
func (p Paths) RemoveRuntime() error {
	files := []string{p.Lock(), p.OldPopupLock(), p.Socket()}
	for _, pattern := range []string{"h-*.sock", "h-*.id", "t-*.sock", "t-*.id", "tts-*.sock"} {
		found, err := filepath.Glob(filepath.Join(p.RuntimeDir, pattern))
		if err != nil {
			return err
		}
		files = append(files, found...)
	}
	for _, f := range files {
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	// Fails when other files are there, which is what's wanted.
	_ = os.Remove(p.RuntimeDir)
	return nil
}

// Socket is the daemon's unix socket.
func (p Paths) Socket() string { return filepath.Join(p.RuntimeDir, "daemon.sock") }

// Lock is the daemon's single-instance lock file.
func (p Paths) Lock() string { return filepath.Join(p.RuntimeDir, "daemon.lock") }

// DB is the SQLite state database.
func (p Paths) DB() string { return filepath.Join(p.StateDir, "state.db") }

// Spool holds hook events written while the daemon was unreachable.
func (p Paths) Spool() string { return filepath.Join(p.StateDir, "events.spool.jsonl") }

// DaemonLog is the daemon's log file.
func (p Paths) DaemonLog() string { return filepath.Join(p.StateDir, "daemon.log") }

// Config is the user's config.toml.
func (p Paths) Config() string { return filepath.Join(p.ConfigDir, "config.toml") }

// Env is the captured login-shell environment.
func (p Paths) Env() string { return filepath.Join(p.StateDir, "env.json") }

// Setup records the last `acmd init` (what it configured, and that it ran).
func (p Paths) Setup() string { return filepath.Join(p.StateDir, "setup.json") }

// Keys is the app's keyboard shortcuts, which it writes for `acmd keys`.
func (p Paths) Keys() string { return filepath.Join(p.StateDir, "keys.json") }

// Backups holds copies of config files from before acmd edited them.
func (p Paths) Backups() string { return filepath.Join(p.StateDir, "backups") }

// TermSocket is the unix socket of session id's terminal server (SPEC
// §8.7). The name is short: unix socket paths are limited to 104 bytes.
func (p Paths) TermSocket(id string) string {
	return filepath.Join(p.RuntimeDir, "t-"+termKey(id)+".sock")
}

// TermIDFile holds the full session id next to TermSocket, so a daemon
// that restarts can find the terminals that are still running.
func (p Paths) TermIDFile(id string) string {
	return filepath.Join(p.RuntimeDir, "t-"+termKey(id)+".id")
}

// HostSocket is the unix socket of session id's PTY host: the
// process that runs the session's CLI. It sits next to the terminal sockets
// with an "h-" prefix.
func (p Paths) HostSocket(id string) string {
	return filepath.Join(p.RuntimeDir, "h-"+termKey(id)+".sock")
}

// HostIDFile holds the full session id next to HostSocket, so a daemon that
// restarts can find the hosts that are still running.
func (p Paths) HostIDFile(id string) string {
	return filepath.Join(p.RuntimeDir, "h-"+termKey(id)+".id")
}

// termKey is the random tail of a ULID session id (unique enough).
func termKey(id string) string {
	if len(id) > 12 {
		return id[len(id)-12:]
	}
	return id
}
