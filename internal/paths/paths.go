// Package paths resolves where acmd keeps its state and runtime files
// (SPEC §4.4). ACMD_STATE_DIR and ACMD_RUNTIME_DIR override the defaults
// (tests, and running several isolated instances).
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
}

// Default resolves the paths for the current user.
func Default() (Paths, error) {
	state := os.Getenv("ACMD_STATE_DIR")
	if state == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, err
		}
		state = filepath.Join(home, ".local", "state", "agent-command")
	}
	runtime := os.Getenv("ACMD_RUNTIME_DIR")
	if runtime == "" {
		runtime = filepath.Join(os.TempDir(), fmt.Sprintf("agent-command-%d", os.Getuid()))
		if len(filepath.Join(runtime, "daemon.sock")) >= maxSunPath {
			runtime = fmt.Sprintf("/tmp/agent-command-%d", os.Getuid())
		}
	}
	return Paths{StateDir: state, RuntimeDir: runtime}, nil
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
