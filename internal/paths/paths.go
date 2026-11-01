// Package paths resolves where acmd keeps its state and runtime files
// (SPEC §4.4). ACMD_STATE_DIR, ACMD_RUNTIME_DIR and ACMD_CONFIG_DIR
// override the defaults (tests, and running several isolated instances).
package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

// maxSunPath is macOS's limit for unix socket paths (sun_path, incl. NUL).
const maxSunPath = 104

// Paths are acmd's file locations.
type Paths struct {
	StateDir   string // ~/.local/state/agent-command
	RuntimeDir string // $TMPDIR/agent-command-$UID (0700)
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
		state = filepath.Join(home, ".local", "state", "agent-command")
	}
	config := os.Getenv("ACMD_CONFIG_DIR")
	if config == "" {
		config = filepath.Join(home, ".config", "agent-command")
	}
	runtime := os.Getenv("ACMD_RUNTIME_DIR")
	if runtime == "" {
		runtime = filepath.Join(os.TempDir(), fmt.Sprintf("agent-command-%d", os.Getuid()))
		if len(filepath.Join(runtime, "daemon.sock")) >= maxSunPath {
			runtime = fmt.Sprintf("/tmp/agent-command-%d", os.Getuid())
		}
	}
	return Paths{StateDir: state, RuntimeDir: runtime, ConfigDir: config}, nil
}

// Ensure creates both directories with mode 0700.
func (p Paths) Ensure() error {
	for _, d := range []string{p.StateDir, p.RuntimeDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
		// Directories need the execute bit; 0700 is owner-only.
		if err := os.Chmod(d, 0o700); err != nil { //nolint:gosec // G302 targets files; this is a private directory
			return err
		}
	}
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

// Config is the user's config.toml (SPEC §10).
func (p Paths) Config() string { return filepath.Join(p.ConfigDir, "config.toml") }

// Env is the captured login-shell environment (SPEC §9.4).
func (p Paths) Env() string { return filepath.Join(p.StateDir, "env.json") }

// Setup records the last `acmd init` (what it configured, and that it ran).
func (p Paths) Setup() string { return filepath.Join(p.StateDir, "setup.json") }

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

// HostSocket is the unix socket of session id's PTY host (SPEC §4.5): the
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
