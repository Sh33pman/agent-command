// Package service runs acmd's daemon as a per-user service: a systemd user
// unit on Linux (and WSL2), a launchd agent on macOS, a logon task in Task
// Scheduler on Windows (task.go). The service runs as the
// user, so sessions get their login, keys and files.
//
// Everything outside acmd's own files goes through Env, so tests never touch
// the real service manager.
package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// Unit is the systemd user unit's name.
	Unit = "agent-command.service"
	// Label is the launchd agent's label.
	Label = "com.brettmostert.agent-command.daemon"
	// EnvVar marks a daemon the service manager started. The daemon removes it
	// from its own environment, so sessions don't inherit it.
	EnvVar = "ACMD_SERVICE"
)

// The service managers.
const (
	Systemd = "systemd"
	Launchd = "launchd"
)

// ErrUnsupported is returned when this machine has no service manager acmd can
// use. The error's text says why, and what to do.
var ErrUnsupported = errors.New("no service manager")

// Env is the machine the service is installed on.
type Env struct {
	GOOS string
	Home string
	UID  int
	User string
	// Run runs a program (systemctl, loginctl, launchctl) and returns its
	// combined output.
	Run func(ctx context.Context, name string, args ...string) ([]byte, error)
	// Detach starts a shell command that outlives this process, for a
	// restart this process can't wait for (the service's own daemon reloading
	// itself).
	Detach func(script string) error
	// Systemd says systemd is the init system (/run/systemd/system exists).
	Systemd func() bool
	// WSL says this Linux is WSL.
	WSL bool
	// LocalAppData is Windows' %LOCALAPPDATA%: the logon task's definition
	// and the daemon's pid are kept in acmd's folder there.
	LocalAppData string
	// Exists says whether a file is there (the launcher, on Windows).
	Exists func(string) bool
	// Alive says whether pid is still the service's daemon, and Kill stops
	// it: Windows' Task Scheduler keeps no pid to ask for or stop.
	Alive     func(pid int) bool
	Kill      func(pid int) error
	ReadFile  func(string) ([]byte, error)
	WriteFile func(string, []byte, os.FileMode) error
	Remove    func(string) error
}

// Status is the service on this machine, as `acmd service status --json`
// prints it.
type Status struct {
	// Supported is false where acmd can't install a service; Note says why.
	Supported bool   `json:"supported"`
	Manager   string `json:"manager,omitempty"`
	File      string `json:"file,omitempty"`
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Pid       int    `json:"pid,omitempty"`
	// Binary is the acmd the service runs.
	Binary string `json:"binary,omitempty"`
	// Linger is whether systemd starts the user's services at boot, with no
	// one logged in. Only on systemd.
	Linger *bool  `json:"linger,omitempty"`
	Note   string `json:"note,omitempty"`
}

// Manager is which service manager this machine uses, or ErrUnsupported with
// the reason.
func (e Env) Manager() (string, error) {
	switch e.GOOS {
	case "darwin":
		return Launchd, nil
	case "linux":
		if e.Systemd != nil && e.Systemd() {
			return Systemd, nil
		}
		if e.WSL {
			return "", fmt.Errorf("%w: WSL isn't running systemd. To turn it on, add these lines to /etc/wsl.conf:\n\n"+
				"    [boot]\n    systemd=true\n\n"+
				"then run `wsl --shutdown` from Windows, open WSL again and run `acmd service install`. "+
				"Until then the daemon starts when something needs it", ErrUnsupported)
		}
		return "", fmt.Errorf("%w: this Linux isn't running systemd, so acmd can't run the daemon as a service. "+
			"It starts when something needs it", ErrUnsupported)
	case "windows":
		if e.LocalAppData == "" {
			return "", fmt.Errorf("%w: LOCALAPPDATA isn't set, so there's nowhere for the logon task", ErrUnsupported)
		}
		return TaskScheduler, nil
	}
	return "", fmt.Errorf("%w: acmd can't run the daemon as a service on %s", ErrUnsupported, e.GOOS)
}

// File is the unit or agent's path.
func (e Env) File() string {
	if e.GOOS == "windows" {
		return filepath.Join(e.taskDir(), "logon-task.xml")
	}
	if e.GOOS == "darwin" {
		return filepath.Join(e.Home, "Library", "LaunchAgents", Label+".plist")
	}
	return filepath.Join(e.Home, ".config", "systemd", "user", Unit)
}

// Installed says whether the service is installed and its manager is there to
// run it. It runs nothing, so it's cheap enough to ask before every start.
func (e Env) Installed() bool {
	if _, err := e.Manager(); err != nil {
		return false
	}
	_, err := e.ReadFile(e.File())
	return err == nil
}

// Definition is the unit or plist that runs binary.
func (e Env) Definition(binary string) []byte {
	if e.GOOS == "windows" {
		return utf16File(e.task(binary))
	}
	if e.GOOS == "darwin" {
		return plist(binary)
	}
	return unit(binary)
}

// unit is the systemd user unit. KillMode=process stops only the daemon:
// session hosts run in their own sessions and must outlive it. The daemon
// writes daemon.log itself, so the journal gets only what it says before that.
// With acmd gone (`brew uninstall` has no hook to remove the unit), it doesn't
// start.
func unit(binary string) []byte {
	return []byte(`[Unit]
Description=Agent Command daemon
Documentation=https://github.com/brettmostert/homebrew-tap
ConditionFileIsExecutable=` + systemdQuote(binary) + `

[Service]
Type=simple
ExecStart=` + systemdQuote(binary) + ` daemon
Environment=` + EnvVar + `=1
KillMode=process
Restart=always
RestartSec=3

[Install]
WantedBy=default.target
`)
}

// systemdQuote quotes a path for ExecStart when it needs it.
func systemdQuote(s string) string {
	if !strings.ContainsAny(s, " \t\"'\\") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// plist is the launchd agent. AbandonProcessGroup keeps launchd from ending
// session hosts with the daemon. The daemon writes daemon.log itself. It's
// kept alive only while acmd is there: `brew uninstall` has no hook to remove
// the agent, and launchd would otherwise retry a missing binary forever.
func plist(binary string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + Label + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + xmlEscape(binary) + `</string>
		<string>daemon</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>` + EnvVar + `</key>
		<string>1</string>
	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>PathState</key>
		<dict>
			<key>` + xmlEscape(binary) + `</key>
			<true/>
		</dict>
	</dict>
	<key>AbandonProcessGroup</key>
	<true/>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>ThrottleInterval</key>
	<integer>3</integer>
</dict>
</plist>
`)
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace(s)
}

// Binary is the acmd an installed unit or plist runs ("" when it can't tell).
func Binary(def []byte) string {
	s := fromUTF16(def)
	if strings.Contains(s, "<Task ") {
		return taskBinary(s)
	}
	for line := range strings.Lines(s) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "ExecStart="); ok {
			v = strings.TrimSuffix(v, " daemon")
			if unq, err := strconv.Unquote(v); err == nil {
				return unq
			}
			return v
		}
	}
	if _, rest, ok := strings.Cut(s, "<key>ProgramArguments</key>"); ok {
		if _, rest, ok := strings.Cut(rest, "<string>"); ok {
			v, _, _ := strings.Cut(rest, "</string>")
			return strings.NewReplacer("&lt;", "<", "&gt;", ">", "&quot;", `"`, "&apos;", "'", "&amp;", "&").Replace(v)
		}
	}
	return ""
}

// Change is what Install did.
type Change struct {
	// Wrote is true when the unit or plist was created or changed.
	Wrote bool
	// Started is true when the service was started or restarted.
	Started bool
	// Notes are what the user should know or do, such as turning on lingering
	// by hand.
	Notes []string
}

// InstallOptions say how much Install does.
type InstallOptions struct {
	// NoStart only writes and enables the service: it starts at the next
	// boot or login, or when something next needs the daemon.
	NoStart bool
	// Self is true when the service's own daemon is installing: a restart it
	// needs would end it before it answers, so the restart is detached and
	// waits a moment.
	Self bool
}

// Install writes the unit or plist for binary and enables it. Unless told
// not to, it starts the service, or restarts it when its definition changed.
// It's safe to rerun: with nothing to change, it only makes sure the service
// is running.
func (e Env) Install(ctx context.Context, binary string, o InstallOptions) (Change, error) {
	var ch Change
	mgr, err := e.Manager()
	if err != nil {
		return ch, err
	}
	want := e.Definition(binary)
	old, err := e.ReadFile(e.File())
	ch.Wrote = err != nil || !bytes.Equal(old, want)
	if ch.Wrote {
		if err := os.MkdirAll(filepath.Dir(e.File()), 0o700); err != nil {
			return ch, err
		}
		if err := e.WriteFile(e.File(), want, 0o600); err != nil {
			return ch, err
		}
	}
	switch mgr {
	case Systemd:
		return e.installSystemd(ctx, ch, o)
	case TaskScheduler:
		return e.installTask(ctx, binary, ch, o)
	}
	return e.installLaunchd(ctx, ch, o)
}

func (e Env) installSystemd(ctx context.Context, ch Change, o InstallOptions) (Change, error) {
	if err := e.systemctl(ctx, "daemon-reload"); err != nil {
		return ch, err
	}
	if err := e.systemctl(ctx, "enable", Unit); err != nil {
		return ch, err
	}
	if !e.linger(ctx) {
		if _, err := e.Run(ctx, "loginctl", "enable-linger"); err != nil || !e.linger(ctx) {
			ch.Notes = append(ch.Notes, fmt.Sprintf("Couldn't turn on lingering, so the daemon starts at login rather than at boot. "+
				"To start it at boot, run: sudo loginctl enable-linger %s", e.User))
		}
	}
	if o.NoStart {
		return ch, nil
	}
	switch {
	case ch.Wrote && o.Self:
		return ch, e.Detach("sleep 1; systemctl --user restart " + Unit)
	case ch.Wrote:
		ch.Started = true
		return ch, e.systemctl(ctx, "restart", Unit)
	case !e.Running(ctx):
		ch.Started = true
		return ch, e.systemctl(ctx, "start", Unit)
	}
	return ch, nil
}

func (e Env) installLaunchd(ctx context.Context, ch Change, o InstallOptions) (Change, error) {
	if o.NoStart {
		return ch, nil
	}
	loaded := e.loaded(ctx)
	switch {
	case loaded && ch.Wrote && o.Self:
		// launchd reads the plist only when it's loaded: unload and load it,
		// from outside the daemon that bootout ends.
		return ch, e.Detach(fmt.Sprintf("sleep 1; launchctl bootout %s; launchctl bootstrap %s %s",
			e.target(), e.domain(), shellQuote(e.File())))
	case loaded && ch.Wrote:
		_ = e.launchctl(ctx, "bootout", e.target())
		e.waitUnloaded(ctx)
		ch.Started = true
		return ch, e.bootstrap(ctx)
	case loaded:
		if !e.Running(ctx) {
			ch.Started = true
			return ch, e.launchctl(ctx, "kickstart", e.target())
		}
		return ch, nil
	}
	ch.Started = true
	return ch, e.bootstrap(ctx)
}

// bootstrap loads the agent into the login session, which starts it
// (RunAtLoad). With no one logged in at the Mac (only over SSH), there's no
// login session to load it into: it loads at the next login.
func (e Env) bootstrap(ctx context.Context) error {
	out, err := e.Run(ctx, "launchctl", "bootstrap", e.domain(), e.File())
	if err != nil {
		return fmt.Errorf("launchctl bootstrap: %s (it starts at your next login)", oneLine(out, err))
	}
	return nil
}

// waitUnloaded waits up to 3s for launchd to finish unloading the agent: a
// bootstrap before then fails.
func (e Env) waitUnloaded(ctx context.Context) {
	for deadline := time.Now().Add(3 * time.Second); e.loaded(ctx) && time.Now().Before(deadline) && ctx.Err() == nil; {
		time.Sleep(50 * time.Millisecond)
	}
}

// Uninstall stops the service, disables it and removes its file. Lingering
// stays on: other services may need it.
func (e Env) Uninstall(ctx context.Context) (bool, error) {
	mgr, err := e.Manager()
	if err != nil {
		return false, err
	}
	if _, err := e.ReadFile(e.File()); err != nil {
		return false, nil
	}
	switch {
	case mgr == TaskScheduler:
		if err := e.uninstallTask(ctx); err != nil {
			return true, err
		}
	case mgr == Systemd:
		_ = e.systemctl(ctx, "disable", "--now", Unit)
	case e.loaded(ctx):
		_ = e.launchctl(ctx, "bootout", e.target())
		e.waitUnloaded(ctx)
	}
	if err := e.Remove(e.File()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return true, err
	}
	if mgr == Systemd {
		_ = e.systemctl(ctx, "daemon-reload")
	}
	return true, nil
}

// Start starts the service if it isn't running.
func (e Env) Start(ctx context.Context) error {
	mgr, err := e.Manager()
	if err != nil {
		return err
	}
	switch mgr {
	case Systemd:
		return e.systemctl(ctx, "start", Unit)
	case TaskScheduler:
		if e.Running(ctx) {
			return nil
		}
		return e.runTask(ctx)
	}
	if !e.loaded(ctx) {
		return e.bootstrap(ctx)
	}
	return e.launchctl(ctx, "kickstart", e.target())
}

// Stop stops the service until something next needs the daemon (which starts
// it again), or the next boot or login.
func (e Env) Stop(ctx context.Context) error {
	mgr, err := e.Manager()
	if err != nil {
		return err
	}
	switch mgr {
	case Systemd:
		return e.systemctl(ctx, "stop", Unit)
	case TaskScheduler:
		e.stopTaskDaemon()
		return nil
	}
	if !e.loaded(ctx) {
		return nil
	}
	err = e.launchctl(ctx, "bootout", e.target())
	e.waitUnloaded(ctx)
	return err
}

// Restart restarts the service, or starts it if it isn't running.
func (e Env) Restart(ctx context.Context) error {
	mgr, err := e.Manager()
	if err != nil {
		return err
	}
	switch mgr {
	case Systemd:
		return e.systemctl(ctx, "restart", Unit)
	case TaskScheduler:
		e.stopTaskDaemon()
		return e.runTask(ctx)
	}
	if !e.loaded(ctx) {
		return e.bootstrap(ctx)
	}
	return e.launchctl(ctx, "kickstart", "-k", e.target())
}

// StartAfterExit makes sure the service starts its daemon again once this
// one exits. launchd (KeepAlive) and systemd (Restart=always) do that
// themselves; Windows' logon task has no restart rule, so it's run again, a
// moment from now, from outside this process.
func (e Env) StartAfterExit() error {
	mgr, err := e.Manager()
	if err != nil || mgr != TaskScheduler {
		return err
	}
	return e.Detach(fmt.Sprintf(`timeout /T 2 /NOBREAK >NUL & schtasks /Run /TN "%s"`, TaskName))
}

// Running says whether the service's daemon is running.
func (e Env) Running(ctx context.Context) bool {
	return e.Pid(ctx) > 0
}

// Pid is the service's daemon's pid, 0 when it isn't running.
func (e Env) Pid(ctx context.Context) int {
	mgr, err := e.Manager()
	if err != nil {
		return 0
	}
	if mgr == TaskScheduler {
		return e.taskPid()
	}
	if mgr == Systemd {
		out, err := e.Run(ctx, "systemctl", "--user", "show", "--property=MainPID", "--value", Unit)
		if err != nil {
			return 0
		}
		pid, _ := strconv.Atoi(strings.TrimSpace(string(out)))
		return pid
	}
	out, err := e.Run(ctx, "launchctl", "print", e.target())
	if err != nil {
		return 0
	}
	for line := range strings.Lines(string(out)) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "pid = "); ok {
			pid, _ := strconv.Atoi(v)
			return pid
		}
	}
	return 0
}

// Status reports the service.
func (e Env) Status(ctx context.Context) Status {
	mgr, err := e.Manager()
	if err != nil {
		return Status{Note: strings.TrimPrefix(err.Error(), ErrUnsupported.Error()+": ")}
	}
	s := Status{Supported: true, Manager: mgr, File: e.File()}
	if def, err := e.ReadFile(e.File()); err == nil {
		s.Installed = true
		s.Binary = Binary(def)
		s.Pid = e.Pid(ctx)
		s.Running = s.Pid > 0
	}
	if mgr == Systemd {
		l := e.linger(ctx)
		s.Linger = &l
	}
	return s
}

func (e Env) linger(ctx context.Context) bool {
	out, err := e.Run(ctx, "loginctl", "show-user", e.User, "--property=Linger", "--value")
	return err == nil && strings.TrimSpace(string(out)) == "yes"
}

func (e Env) loaded(ctx context.Context) bool {
	_, err := e.Run(ctx, "launchctl", "print", e.target())
	return err == nil
}

func (e Env) domain() string { return "gui/" + strconv.Itoa(e.UID) }
func (e Env) target() string { return e.domain() + "/" + Label }

func (e Env) systemctl(ctx context.Context, args ...string) error {
	out, err := e.Run(ctx, "systemctl", append([]string{"--user"}, args...)...)
	if err != nil {
		return fmt.Errorf("systemctl --user %s: %s", strings.Join(args, " "), oneLine(out, err))
	}
	return nil
}

func (e Env) launchctl(ctx context.Context, args ...string) error {
	out, err := e.Run(ctx, "launchctl", args...)
	if err != nil {
		return fmt.Errorf("launchctl %s: %s", strings.Join(args, " "), oneLine(out, err))
	}
	return nil
}

// oneLine is a command's output for an error message, or the error when it
// said nothing.
func oneLine(out []byte, err error) string {
	if s := strings.Join(strings.Fields(string(out)), " "); s != "" {
		return s
	}
	return err.Error()
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
