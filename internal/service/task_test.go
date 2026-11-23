package service_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/service"
	"github.com/brettmostert/agent-command/internal/service/servicetest"
)

// windowsAcmd is an acmd.exe with the launcher next to it, as Scoop installs
// them.
func windowsAcmd(t *testing.T, home string) string {
	t.Helper()
	dir := filepath.Join(home, "scoop", "apps", "acmd", "current")
	must(t, os.MkdirAll(dir, 0o700))
	for _, f := range []string{"acmd.exe", service.Launcher} {
		must(t, os.WriteFile(filepath.Join(dir, f), nil, 0o600))
	}
	return filepath.Join(dir, "acmd.exe")
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestTaskContents(t *testing.T) {
	home := t.TempDir()
	f := servicetest.New("windows", home)
	e := f.Env()
	acmd := windowsAcmd(t, home)
	def := e.Definition(acmd)
	if def[0] != 0xff || def[1] != 0xfe {
		t.Fatalf("not UTF-16 with a BOM, as Task Scheduler reads it: % x", def[:4])
	}
	if got := service.Binary(def); got != acmd {
		t.Errorf("Binary = %q, want %q", got, acmd)
	}
	// What the task is, as Task Scheduler's XML.
	var text strings.Builder
	for i := 2; i+1 < len(def); i += 2 {
		text.WriteByte(def[i]) // the XML here is ASCII
	}
	xml := text.String()
	for _, want := range []string{
		"<LogonTrigger>", "<UserId>tester</UserId>",
		// As you, with no password stored and no elevation.
		"<LogonType>InteractiveToken</LogonType>", "<RunLevel>LeastPrivilege</RunLevel>",
		// No time limit: the default ends a task after 72 hours.
		"<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>",
		"<DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>",
		// The hidden launcher, with the pid file and the acmd it starts.
		"<Command>" + filepath.Join(filepath.Dir(acmd), service.Launcher) + "</Command>",
		"<Arguments>&quot;" + e.PidFile() + "&quot; &quot;" + acmd + "&quot; daemon</Arguments>",
	} {
		if !strings.Contains(xml, want) {
			t.Errorf("task lacks %q:\n%s", want, xml)
		}
	}
}

func TestTaskInstallUninstall(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	f := servicetest.New("windows", home)
	e := f.Env()
	if mgr, err := e.Manager(); err != nil || mgr != service.TaskScheduler {
		t.Fatalf("Manager = %q, %v", mgr, err)
	}
	acmd := windowsAcmd(t, home)

	ch, err := e.Install(ctx, acmd, service.InstallOptions{})
	if err != nil || !ch.Wrote || !ch.Started || !f.Registered || !f.Running() {
		t.Fatalf("install: %+v %v, registered %v running %v", ch, err, f.Registered, f.Running())
	}
	if !e.Installed() || !e.Running(ctx) || e.Pid(ctx) != f.Pid {
		t.Errorf("installed %v running %v pid %d (fake %d)", e.Installed(), e.Running(ctx), e.Pid(ctx), f.Pid)
	}
	st := e.Status(ctx)
	if !st.Supported || st.Manager != service.TaskScheduler || !st.Installed || !st.Running || st.Binary != acmd || st.Linger != nil {
		t.Errorf("status = %+v", st)
	}

	// Again, nothing to change: it keeps the daemon it has.
	pid := f.Pid
	if ch, err := e.Install(ctx, acmd, service.InstallOptions{}); err != nil || ch.Wrote || ch.Started || f.Pid != pid {
		t.Errorf("rerun: %+v %v", ch, err)
	}
	// Stopped, it starts again; restarted, it's a new daemon.
	must(t, e.Stop(ctx))
	if f.Running() || e.Running(ctx) {
		t.Error("still running after Stop")
	}
	must(t, e.Start(ctx))
	if !f.Running() {
		t.Error("not running after Start")
	}
	pid = f.Pid
	must(t, e.Restart(ctx))
	if !f.Running() || f.Pid == pid || e.Pid(ctx) != f.Pid {
		t.Errorf("restart: pid %d → %d", pid, f.Pid)
	}

	// A new acmd: the task changes, and its daemon is the new one.
	newer := windowsAcmd(t, filepath.Join(home, "other"))
	pid = f.Pid
	ch, err = e.Install(ctx, newer, service.InstallOptions{})
	if err != nil || !ch.Wrote || !ch.Started || f.Pid == pid || e.Status(ctx).Binary != newer {
		t.Errorf("new binary: %+v %v", ch, err)
	}

	if removed, err := e.Uninstall(ctx); err != nil || !removed || f.Registered || f.Running() || e.Installed() {
		t.Errorf("uninstall: %v %v, registered %v running %v", removed, err, f.Registered, f.Running())
	}
	if _, err := os.Stat(e.PidFile()); !os.IsNotExist(err) {
		t.Errorf("pid file: %v", err)
	}
	if removed, err := e.Uninstall(ctx); err != nil || removed {
		t.Errorf("uninstall again: %v %v", removed, err)
	}
}

func TestTaskNeedsTheLauncher(t *testing.T) {
	home := t.TempDir()
	f := servicetest.New("windows", home)
	acmd := windowsAcmd(t, home)
	must(t, os.Remove(filepath.Join(filepath.Dir(acmd), service.Launcher)))
	if _, err := f.Env().Install(context.Background(), acmd, service.InstallOptions{}); err == nil || !strings.Contains(err.Error(), service.Launcher) {
		t.Errorf("install without the launcher: %v", err)
	}
	if f.Registered {
		t.Error("registered a task that can't start")
	}
}

func TestTaskSelfRestartIsDetached(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	f := servicetest.New("windows", home)
	e := f.Env()
	_, err := e.Install(ctx, windowsAcmd(t, home), service.InstallOptions{})
	must(t, err)
	pid := f.Pid
	ch, err := e.Install(ctx, windowsAcmd(t, filepath.Join(home, "new")), service.InstallOptions{Self: true})
	if err != nil || !ch.Wrote || ch.Started || f.Pid != pid {
		t.Errorf("self: %+v %v", ch, err)
	}
	d := f.Detached()
	if len(d) != 1 || !strings.HasPrefix(d[0], "timeout /T 1 ") || !strings.Contains(d[0], "taskkill /F /PID ") ||
		!strings.HasSuffix(d[0], `schtasks /Run /TN "`+service.TaskName+`"`) {
		t.Errorf("detached %q", d)
	}
}

// A pid file left by a daemon that's gone, or now another program's, isn't
// the service's daemon.
func TestTaskStalePid(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	f := servicetest.New("windows", home)
	e := f.Env()
	must(t, os.MkdirAll(filepath.Dir(e.PidFile()), 0o700))
	must(t, os.WriteFile(e.PidFile(), []byte("4242\n"), 0o600))
	if e.Pid(ctx) != 0 || e.Running(ctx) {
		t.Error("a stale pid counts as running")
	}
	must(t, os.WriteFile(e.PidFile(), []byte("nonsense"), 0o600))
	if e.Pid(ctx) != 0 {
		t.Error("a garbled pid file counts")
	}
}

func TestTaskWithoutLocalAppData(t *testing.T) {
	e := servicetest.New("windows", t.TempDir()).Env()
	e.LocalAppData = ""
	if _, err := e.Manager(); err == nil {
		t.Error("a task with nowhere to keep it")
	}
	if slices.Contains([]string{e.Status(context.Background()).Manager}, service.TaskScheduler) {
		t.Error("status names Task Scheduler")
	}
}

// A daemon making way for a new build: launchd and systemd start it again
// themselves; Windows' task is run again from outside it.
func TestStartAfterExit(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		f := servicetest.New(goos, t.TempDir())
		if err := f.Env().StartAfterExit(); err != nil || len(f.Detached()) != 0 {
			t.Errorf("%s: %v, detached %q", goos, err, f.Detached())
		}
	}
	f := servicetest.New("windows", t.TempDir())
	must(t, f.Env().StartAfterExit())
	if d := f.Detached(); len(d) != 1 || !strings.HasPrefix(d[0], "timeout /T 2 ") || !strings.HasSuffix(d[0], `schtasks /Run /TN "`+service.TaskName+`"`) {
		t.Errorf("windows: detached %q", d)
	}
	plain := servicetest.New("linux", t.TempDir())
	plain.NoSystemd = true
	if err := plain.Env().StartAfterExit(); err == nil {
		t.Error("no service manager, and no error")
	}
}
