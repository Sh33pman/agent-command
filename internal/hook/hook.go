// Package hook implements `acmd hook <Event>`, which Claude Code runs for
// every hook event, and Antigravity's CLI (agy) for its events as
// `acmd hook antigravity:<Event>`. It only acts for an agent running in one
// of acmd's sessions. It must be fast and fail-open: it
// never writes to stdout, never influences the agent's decisions and always
// exits 0. (agy needs a JSON answer on stdout: the command prints a neutral
// `{}` for it, see AntigravityReply.) If the daemon is unreachable (or a
// different version) the event is spooled and a fresh daemon is started.
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
	TTY  string // controlling terminal, e.g. /dev/ttys010 ("" = none)
}

// Options configures Run; zero values use the real environment.
type Options struct {
	Paths   paths.Paths
	Version string
	Stdin   io.Reader
	Getenv  func(string) string
	Now     func() time.Time
	// ParentPid is where the walk up to the agent's process starts.
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
	Ignored  Outcome = "ignored" // headless `claude -p` or `agy -p`
	Rejected Outcome = "rejected"
)

// Run handles one hook invocation.
func Run(name string, o Options) (Outcome, error) {
	o.defaults()
	// Hooks fire for every Claude on the machine. Only one running in a session
	// of ours is our business: nothing else may reach or start the daemon.
	if o.Getenv("ACMD_SESSION_ID") == "" {
		return Ignored, nil
	}
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

	hc, headless := o.context(core.IsAntigravityHook(name))
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
	// After an uninstall there's nothing to report to. A Claude ended by it fires
	// a last hook, which mustn't bring the daemon back.
	if _, err := os.Stat(o.Paths.Setup()); err != nil {
		return Ignored, nil
	}
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

// AntigravityReply is what `acmd hook antigravity:<Event>` prints: agy reads
// every hook's answer from stdout, and an empty object changes nothing (a Stop
// hook that doesn't say "continue" lets the agent stop).
const AntigravityReply = "{}\n"

// context gathers terminal/process context, and reports whether the
// agent's process is headless (`claude -p` / `--print`, `agy -p`), which acmd
// ignores.
func (o *Options) context(agy bool) (ipc.HookContext, bool) {
	hc := ipc.HookContext{HostSession: o.Getenv("ACMD_SESSION_ID")}
	name, headless := "claude", IsHeadless
	if agy {
		name, headless = "agy", IsAgyHeadless
	}
	p, ok := findAgent(o.ParentPid, o.ProcInfo, name)
	if !ok {
		return hc, false
	}
	hc.ClaudePid = p.Pid
	return hc, headless(p.Args)
}

// findAgent walks up from pid to the nearest process named name (hooks run in
// a shell started by the agent).
func findAgent(pid int, info func(int) (Proc, error), name string) (Proc, bool) {
	for i := 0; i < 8 && pid > 1; i++ {
		p, err := info(pid)
		if err != nil {
			return Proc{}, false
		}
		if p.Comm == name || len(p.Args) > 0 && filepath.Base(p.Args[0]) == name {
			return p, true
		}
		pid = p.PPid
	}
	return Proc{}, false
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

// IsAgyHeadless reports whether agy was started in print mode: -p, --print,
// or --prompt, its alias. -i takes the first prompt as its value, which may look
// like a flag.
func IsAgyHeadless(args []string) bool {
	rest := args[min(1, len(args)):]
	for i := 0; i < len(rest); i++ {
		arg := rest[i]
		if arg == "--" {
			return false
		}
		if !strings.HasPrefix(arg, "-") {
			continue
		}
		// agy takes -flag and --flag alike.
		name, _, hasValue := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-"), "=")
		switch name {
		case "i", "prompt-interactive":
			if !hasValue {
				i++ // its value
			}
		case "p", "print", "prompt":
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
