// Package hook implements `acmd hook <Event>`, which Claude Code runs for
// every hook event (SPEC §4.1, §9.1). It must be fast and fail-open: it
// never writes to stdout, never influences Claude's decisions and always
// exits 0. If the daemon is unreachable (or a different version) the event
// is spooled and a fresh daemon is started.
package hook

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/procenv"
)

// Proc is a process as seen by the hook.
type Proc struct {
	Pid  int
	PPid int
	Comm string
	Args []string
}

// Options configures Run; zero values use the real environment.
type Options struct {
	Paths   paths.Paths
	Version string
	Stdin   io.Reader
	Getenv  func(string) string
	Now     func() time.Time
	// ParentPid is where the walk up to the claude process starts.
	ParentPid int
	// ProcInfo looks up a process (default: sysctl on macOS).
	ProcInfo func(pid int) (Proc, error)
	// Spawn starts a detached daemon (default: re-exec this binary).
	Spawn func(paths.Paths) error
	// Timeout bounds talking to the daemon.
	Timeout time.Duration
}

// Outcome says what Run did (for tests and debug logging).
type Outcome string

// Outcomes.
const (
	Sent     Outcome = "sent"
	Spooled  Outcome = "spooled"
	Ignored  Outcome = "ignored" // headless `claude -p`
	Rejected Outcome = "rejected"
)

// Run handles one hook invocation.
func Run(name string, o Options) (Outcome, error) {
	o.defaults()
	at := o.Now()

	raw, err := io.ReadAll(io.LimitReader(o.Stdin, core.MaxHookPayload+1))
	if err != nil {
		return Rejected, err
	}
	if len(raw) > core.MaxHookPayload {
		return Rejected, core.ErrPayloadTooLarge
	}
	if !json.Valid(raw) {
		return Rejected, errors.New("hook payload is not JSON")
	}

	hc, headless := o.context()
	if headless {
		return Ignored, nil
	}
	ev := ipc.HookEvent{Name: name, At: at, Payload: raw, Context: hc}

	ctx, cancel := context.WithTimeout(context.Background(), o.Timeout)
	defer cancel()
	conn, err := ipc.Dial(ctx, o.Paths.Socket(), ipc.Hello{Proto: ipc.Proto, Version: o.Version})
	var mismatch *ipc.ErrVersionMismatch
	switch {
	case err == nil:
		defer conn.Close()
		if err := conn.Call(ipc.OpEvent, ev, nil); err != nil {
			return o.spoolAndSpawn(ev, err)
		}
		return Sent, nil
	case errors.As(err, &mismatch):
		// An older (or newer) daemon from before an upgrade: replace it.
		_ = conn.Call(ipc.OpShutdown, nil, nil)
		_ = conn.Close()
		return o.spoolAndSpawn(ev, err)
	default:
		return o.spoolAndSpawn(ev, err)
	}
}

func (o *Options) spoolAndSpawn(ev ipc.HookEvent, cause error) (Outcome, error) {
	if err := Spool(o.Paths, ev); err != nil {
		return Rejected, errors.Join(cause, err)
	}
	if err := o.Spawn(o.Paths); err != nil {
		return Spooled, errors.Join(cause, err)
	}
	return Spooled, nil
}

// Spool appends an event to the spool file (0600) for the daemon to drain.
func Spool(p paths.Paths, ev ipc.HookEvent) error {
	if err := p.Ensure(); err != nil {
		return err
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Clean(p.Spool()), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// context gathers terminal/process context, and reports whether the
// Claude process is headless (`claude -p` / `--print`), which acmd ignores
// (SPEC §6.8).
func (o *Options) context() (ipc.HookContext, bool) {
	hc := ipc.HookContext{
		KittyListenOn: o.Getenv("KITTY_LISTEN_ON"),
		KittyWindowID: o.Getenv("KITTY_WINDOW_ID"),
		TermProgram:   o.Getenv("TERM_PROGRAM"),
		ViewID:        o.Getenv("ACMD_VIEW_ID"),
	}
	p, ok := findClaude(o.ParentPid, o.ProcInfo)
	if !ok {
		return hc, false
	}
	hc.ClaudePid = p.Pid
	return hc, IsHeadless(p.Args)
}

// findClaude walks up from pid to the nearest claude process (hooks run in
// a shell started by claude).
func findClaude(pid int, info func(int) (Proc, error)) (Proc, bool) {
	for i := 0; i < 8 && pid > 1; i++ {
		p, err := info(pid)
		if err != nil {
			return Proc{}, false
		}
		if isClaude(p) {
			return p, true
		}
		pid = p.PPid
	}
	return Proc{}, false
}

func isClaude(p Proc) bool {
	if p.Comm == "claude" {
		return true
	}
	return len(p.Args) > 0 && filepath.Base(p.Args[0]) == "claude"
}

// IsHeadless reports whether claude was started in print mode.
func IsHeadless(args []string) bool {
	for _, a := range args[min(1, len(args)):] {
		if a == "--" {
			return false
		}
		// Only exact flags: a heuristic for combined short flags (-dp) could
		// misread a prompt argument and drop a real session.
		if a == "-p" || a == "--print" || strings.HasPrefix(a, "--print=") {
			return true
		}
	}
	return false
}

func (o *Options) defaults() {
	if o.Stdin == nil {
		o.Stdin = os.Stdin
	}
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.ParentPid == 0 {
		o.ParentPid = os.Getppid()
	}
	if o.ProcInfo == nil {
		o.ProcInfo = procInfo
	}
	if o.Spawn == nil {
		o.Spawn = SpawnDaemon
	}
	if o.Timeout == 0 {
		o.Timeout = 500 * time.Millisecond
	}
}

// SpawnDaemon starts `acmd daemon` detached (new session, no stdio except
// the daemon log) with a scrubbed environment (M0 C5).
func SpawnDaemon(p paths.Paths) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := p.Ensure(); err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Clean(p.DaemonLog()), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(exe, "daemon") // #nosec G204 -- our own binary
	cmd.Env = procenv.Scrub(os.Environ())
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
