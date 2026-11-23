package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Windows: a per-user Task Scheduler task starts the daemon at logon. A
// Windows service (SCM) isn't used: it would run as another account, or need
// your password. The task runs acmdw.exe, a launcher with no console window
// (built with -H=windowsgui), which starts `acmd.exe daemon` hidden, with
// ACMD_SERVICE=1, writes its pid to PidFile and exits. Task Scheduler has no
// pid of its own to ask for, so PidFile is how the service's daemon is told
// from one outside it.

// TaskName is the logon task's name in Task Scheduler.
const TaskName = "Agent Command daemon"

// Launcher is the hidden launcher's file name, next to acmd.exe.
const Launcher = "acmdw.exe"

// TaskScheduler is Windows' service manager here.
const TaskScheduler = "taskscheduler"

// taskDir is where the task's definition and the daemon's pid are kept: acmd's
// own folder, whose access list is only yours (internal/paths).
func (e Env) taskDir() string { return filepath.Join(e.LocalAppData, "agent-command") }

// PidFile is where the launcher writes the service's daemon's pid.
func (e Env) PidFile() string { return filepath.Join(e.taskDir(), "service.pid") }

// task is the logon task's definition, as Task Scheduler's XML: at your logon,
// as you (no password stored, no elevation), with no time limit (the default
// ends a task after 72 hours), run the launcher, which starts acmd hidden.
func (e Env) task(binary string) string {
	launcher := filepath.Join(filepath.Dir(binary), Launcher)
	return `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>Starts acmd's daemon at logon, with no window: it connects to your hosts and passes on their notices.</Description>
    <URI>\` + xmlEscape(TaskName) + `</URI>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
      <UserId>` + xmlEscape(e.User) + `</UserId>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <UserId>` + xmlEscape(e.User) + `</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>false</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <IdleSettings>
      <StopOnIdleEnd>false</StopOnIdleEnd>
      <RestartOnIdle>false</RestartOnIdle>
    </IdleSettings>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <RunOnlyIfIdle>false</RunOnlyIfIdle>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>7</Priority>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>` + xmlEscape(launcher) + `</Command>
      <Arguments>` + xmlEscape(windowsQuote(e.PidFile())+" "+windowsQuote(binary)+" daemon") + `</Arguments>
    </Exec>
  </Actions>
</Task>
`
}

// windowsQuote quotes an argument as Windows programs split their command
// line (a path with no quote or trailing backslash in it).
func windowsQuote(s string) string { return `"` + s + `"` }

// utf16File is text as Task Scheduler reads an XML file: UTF-16LE, with a BOM.
func utf16File(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2+2*len(u))
	b[0], b[1] = 0xff, 0xfe
	for i, r := range u {
		binary.LittleEndian.PutUint16(b[2+2*i:], r)
	}
	return b
}

// fromUTF16 is a UTF-16LE file with a BOM as text, or b as it is without one.
func fromUTF16(b []byte) string {
	if len(b) < 2 || b[0] != 0xff || b[1] != 0xfe {
		return string(b)
	}
	b = b[2:]
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(b[2*i:])
	}
	return string(utf16.Decode(u))
}

// taskBinary is the acmd a task definition runs: the second argument.
func taskBinary(def string) string {
	_, rest, ok := strings.Cut(def, "<Arguments>")
	if !ok {
		return ""
	}
	args, _, _ := strings.Cut(rest, "</Arguments>")
	args = strings.NewReplacer("&quot;", `"`, "&amp;", "&", "&lt;", "<", "&gt;", ">", "&apos;", "'").Replace(args)
	parts := strings.Split(args, `"`) // "pid" "acmd" daemon → ["", pid, " ", acmd, " daemon"]
	if len(parts) < 4 {
		return ""
	}
	return parts[3]
}

func (e Env) installTask(ctx context.Context, binary string, ch Change, o InstallOptions) (Change, error) {
	launcher := filepath.Join(filepath.Dir(binary), Launcher)
	if e.Exists != nil && !e.Exists(launcher) {
		return ch, fmt.Errorf("%s isn't next to %s: reinstall acmd (scoop install acmd)", Launcher, binary)
	}
	if err := e.schtasks(ctx, "/Create", "/TN", TaskName, "/XML", e.File(), "/F"); err != nil {
		return ch, err
	}
	if o.NoStart {
		return ch, nil
	}
	switch {
	case ch.Wrote && o.Self:
		// This daemon answers first: then it's stopped, and the task starts
		// the new one.
		return ch, e.Detach(fmt.Sprintf(`timeout /T 1 /NOBREAK >NUL & taskkill /F /PID %d & schtasks /Run /TN "%s"`, e.Pid(ctx), TaskName))
	case ch.Wrote:
		e.stopTaskDaemon()
		ch.Started = true
		return ch, e.runTask(ctx)
	case !e.Running(ctx):
		ch.Started = true
		return ch, e.runTask(ctx)
	}
	return ch, nil
}

func (e Env) uninstallTask(ctx context.Context) error {
	e.stopTaskDaemon()
	out, err := e.Run(ctx, "schtasks", "/Delete", "/TN", TaskName, "/F")
	if err != nil && !strings.Contains(strings.ToLower(string(out)), "cannot find") {
		return fmt.Errorf("schtasks /Delete: %s", oneLine(out, err))
	}
	if err := e.Remove(e.PidFile()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// runTask starts the task now, as at logon.
func (e Env) runTask(ctx context.Context) error { return e.schtasks(ctx, "/Run", "/TN", TaskName) }

// stopTaskDaemon stops the service's daemon. The task itself has ended
// already: the launcher exits once the daemon has started.
func (e Env) stopTaskDaemon() {
	if pid := e.taskPid(); pid > 0 && e.Kill != nil {
		_ = e.Kill(pid)
	}
	_ = e.Remove(e.PidFile())
}

// taskPid is the service's daemon's pid from PidFile, 0 when it isn't running.
func (e Env) taskPid() int {
	b, err := e.ReadFile(e.PidFile())
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(string(bytes.TrimSpace(b)))
	if err != nil || pid <= 0 || e.Alive == nil || !e.Alive(pid) {
		return 0
	}
	return pid
}

func (e Env) schtasks(ctx context.Context, args ...string) error {
	out, err := e.Run(ctx, "schtasks", args...)
	if err != nil {
		return fmt.Errorf("schtasks %s: %s", args[0], oneLine(out, err))
	}
	return nil
}
