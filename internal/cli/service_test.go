package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/daemon"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/service"
	"github.com/brettmostert/agent-command/internal/service/servicetest"
	"github.com/brettmostert/agent-command/internal/version"
)

// withService gives the test a fake service manager for goos, with its files
// in the test's home.
func withService(t *testing.T, goos string) *servicetest.Fake {
	t.Helper()
	f := servicetest.New(goos, os.Getenv("HOME"))
	orig := localService
	localService = func() (service.Env, error) { return f.Env(), nil }
	t.Cleanup(func() { localService = orig })
	return f
}

func TestServiceInstallStatusUninstall(t *testing.T) {
	sessionsHere(t)
	newSandbox(t)
	f := withService(t, "linux")
	out, _, err := execute(t, "", "service", "status")
	if err != nil || !strings.Contains(out, "isn't installed") {
		t.Fatalf("status before install: %v\n%s", err, out)
	}
	out, _, err = execute(t, "", "service", "install")
	if err != nil {
		t.Fatal(err)
	}
	if !f.Running() || !f.Enabled || !f.Linger {
		t.Errorf("after install: running %v, enabled %v, linger %v", f.Running(), f.Enabled, f.Linger)
	}
	for _, want := range []string{"installed and running", "agent-command.service", testArgv0, "at boot"} {
		if !strings.Contains(out, want) {
			t.Errorf("install output lacks %q:\n%s", want, out)
		}
	}

	out, _, err = execute(t, "", "service", "status", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var s service.Status
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if !s.Supported || !s.Installed || !s.Running || s.Binary != testArgv0 || s.Manager != "systemd" {
		t.Errorf("status --json: %+v", s)
	}

	out, _, err = execute(t, "", "service", "uninstall")
	if err != nil || !strings.Contains(out, "Removed the service") || f.Running() || f.Enabled {
		t.Errorf("uninstall: %v\n%s", err, out)
	}
	if out, _, _ := execute(t, "", "service", "uninstall"); !strings.Contains(out, "wasn't installed") {
		t.Errorf("uninstall again: %s", out)
	}
}

func TestServiceInstallExplainsWSLWithoutSystemd(t *testing.T) {
	newSandbox(t)
	f := withService(t, "linux")
	f.NoSystemd, f.WSL = true, true
	_, _, err := execute(t, "", "service", "install")
	if err == nil || !strings.Contains(err.Error(), "systemd=true") || !strings.Contains(err.Error(), "wsl --shutdown") {
		t.Errorf("err = %v", err)
	}
	// init still sets up the rest, and says the same.
	out, _, err := execute(t, "", "init")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"skip      the service: WSL isn't running systemd.", "[boot]", "starts when something needs it."} {
		if !strings.Contains(out, want) {
			t.Errorf("init output lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(f.Home, ".config/systemd/user", service.Unit)); !os.IsNotExist(err) {
		t.Errorf("unit written without systemd: %v", err)
	}
	out, _, _ = execute(t, "", "service", "status")
	if !strings.HasPrefix(out, "WSL isn't running systemd") {
		t.Errorf("status: %s", out)
	}
}

func TestInitInstallsTheService(t *testing.T) {
	sessionsHere(t)
	for _, goos := range []string{"linux", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			newSandbox(t)
			f := withService(t, goos)
			out, _, err := execute(t, "", "init", "--dry-run")
			if err != nil || !strings.Contains(out, "  create    ~/") || f.Running() || len(f.Calls()) != 0 {
				t.Fatalf("dry run: %v, calls %q\n%s", err, f.Calls(), out)
			}
			if _, _, err := execute(t, "", "init"); err != nil {
				t.Fatal(err)
			}
			if !f.Running() || !f.Env().Installed() {
				t.Fatalf("init didn't start the service: %q", f.Calls())
			}
			pid := f.Pid
			out, _, err = execute(t, "", "init")
			if err != nil || !strings.Contains(out, "Already set up") || !strings.Contains(out, "unchanged") || f.Pid != pid {
				t.Errorf("rerun: %v, pid %d → %d\n%s", err, pid, f.Pid, out)
			}
			out, _, err = execute(t, "", "uninstall", "--yes")
			if err != nil || !strings.Contains(out, "the service that runs the daemon") {
				t.Fatalf("uninstall: %v\n%s", err, out)
			}
			if f.Running() || f.Env().Installed() {
				t.Errorf("uninstall left the service: %q", f.Calls())
			}
		})
	}
}

func TestInitSaysWhenLingeringNeedsSudo(t *testing.T) {
	newSandbox(t)
	f := withService(t, "linux")
	f.LingerFails = true
	out, _, err := execute(t, "", "init")
	if err != nil || !strings.Contains(out, "sudo loginctl enable-linger tester") {
		t.Errorf("%v\n%s", err, out)
	}
}

func TestDaemonHandsOffToTheService(t *testing.T) {
	newSandbox(t)
	f := withService(t, "linux")
	if _, err := f.Env().Install(context.Background(), "/bin/acmd", service.InstallOptions{NoStart: true}); err != nil {
		t.Fatal(err)
	}
	_, errOut, err := execute(t, "", "daemon")
	if err != nil || !f.Running() || !strings.Contains(errOut, "started the service") {
		t.Errorf("%v, running %v: %s", err, f.Running(), errOut)
	}
}

// shortPaths are folders short enough for a socket, set for the test with a
// home of their own.
func shortPaths(t *testing.T) paths.Paths {
	t.Helper()
	dir, err := os.MkdirTemp("", "acmdsvc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	p := paths.Paths{StateDir: filepath.Join(dir, "s"), RuntimeDir: filepath.Join(dir, "r"), ConfigDir: filepath.Join(dir, "c")}
	t.Setenv("HOME", filepath.Join(dir, "h"))
	t.Setenv("ACMD_STATE_DIR", p.StateDir)
	t.Setenv("ACMD_RUNTIME_DIR", p.RuntimeDir)
	t.Setenv("ACMD_CONFIG_DIR", p.ConfigDir)
	return p
}

// testDaemon runs a daemon of a version in this process until stop.
type testDaemon struct {
	cancel context.CancelFunc
	done   chan error
	err    error // what Run returned
}

func startTestDaemon(t *testing.T, p paths.Paths, ver string) *testDaemon {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	d := &testDaemon{cancel: cancel, done: make(chan error, 1)}
	go func() {
		d.done <- daemon.Run(ctx, daemon.Config{Paths: p, Version: ver, Log: log.New(io.Discard, "", 0),
			TailEvery: time.Hour, ReconcileEvery: time.Hour, LockWait: 3 * time.Second})
	}()
	for deadline := time.Now().Add(5 * time.Second); ; {
		dctx, dcancel := context.WithTimeout(ctx, time.Second)
		conn, err := ipc.Dial(dctx, p.Socket(), ipc.HelloFrom(ver))
		dcancel()
		if err == nil {
			_ = conn.Close()
			return d
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon %s didn't start: %v", ver, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (d *testDaemon) stop() {
	d.cancel()
	d.err = <-d.done
}

// serviceRunning makes the fake service run a daemon of ver in this process.
func serviceRunning(t *testing.T, f *servicetest.Fake, p paths.Paths, ver func() string) {
	t.Helper()
	var running *testDaemon
	f.UsePid = os.Getpid()
	f.OnStart = func() { running = startTestDaemon(t, p, ver()) }
	f.OnStop = func() {
		if running != nil {
			running.stop()
			running = nil
		}
	}
	t.Cleanup(func() { f.OnStop() })
}

func TestUpgradeRestartsThroughTheService(t *testing.T) {
	p := shortPaths(t)
	f := withService(t, "darwin")
	builds := []string{"0.0.1-old", version.Get().Version}
	serviceRunning(t, f, p, func() string {
		v := builds[0]
		if len(builds) > 1 {
			builds = builds[1:]
		}
		return v
	})
	if _, err := f.Env().Install(context.Background(), "/bin/acmd", service.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	// spawnDaemon fails in tests: the new daemon must come from the service.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	if c.conn.Daemon.Version != version.Get().Version {
		t.Errorf("connected to %s", c.conn.Daemon.Version)
	}
	if !slices.Contains(f.Calls(), "launchctl kickstart -k gui/501/"+service.Label) {
		t.Errorf("not restarted through launchd: %q", f.Calls())
	}
}

func TestServiceOfAnotherBuildIsAnError(t *testing.T) {
	p := shortPaths(t)
	f := withService(t, "linux")
	serviceRunning(t, f, p, func() string { return "0.0.1-other" })
	if _, err := f.Env().Install(context.Background(), "/opt/other/acmd", service.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	before := len(f.Calls())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := connect(ctx)
	if err == nil || !strings.Contains(err.Error(), "the service runs /opt/other/acmd (acmd 0.0.1-other)") ||
		!strings.Contains(err.Error(), "acmd service install") {
		t.Fatalf("err = %v", err)
	}
	restarts := 0
	for _, c := range f.Calls()[before:] {
		if strings.HasPrefix(c, "systemctl --user restart") {
			restarts++
		}
	}
	if restarts != 1 {
		t.Errorf("restarted %d times: %q", restarts, f.Calls())
	}
}

func TestDaemonStopRestartThroughTheService(t *testing.T) {
	p := shortPaths(t)
	f := withService(t, "linux")
	serviceRunning(t, f, p, func() string { return version.Get().Version })
	if _, err := f.Env().Install(context.Background(), "/bin/acmd", service.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	cmd := func(args ...string) string {
		t.Helper()
		out, err := run(t, "acmd", append([]string{"daemon"}, args...)...)
		if err != nil {
			t.Fatalf("daemon %v: %v\n%s", args, err, out)
		}
		return out
	}
	pid := f.Pid
	if out := cmd("restart"); !strings.Contains(out, "Restarted the daemon through the service") || !f.Running() {
		t.Errorf("restart: %q", out)
	}
	if !slices.Contains(f.Calls(), "systemctl --user restart "+service.Unit) || f.Pid != pid {
		t.Errorf("calls: %q", f.Calls())
	}
	if out := cmd("stop"); !strings.Contains(out, "Stopped the daemon") || f.Running() || daemon.Running(p) {
		t.Errorf("stop: %q", out)
	}
	if out := cmd("stop"); !strings.Contains(out, "wasn't running") {
		t.Errorf("stop again: %q", out)
	}
	if out := cmd("restart"); !strings.Contains(out, "Started the daemon through the service") {
		t.Errorf("restart when stopped: %q", out)
	}
}

func TestInitHandsOverFromADaemonOutsideTheService(t *testing.T) {
	p := shortPaths(t)
	f := withService(t, "linux")
	outside := startTestDaemon(t, p, version.Get().Version)
	stopped := make(chan struct{})
	go func() { outside.err = <-outside.done; close(stopped) }()
	svc := f.Env()
	if _, err := svc.Install(context.Background(), "/bin/acmd", service.InstallOptions{NoStart: true}); err != nil {
		t.Fatal(err)
	}
	// The fake's service daemon has another pid: the one running is outside it.
	if err := handOver(context.Background(), p, svc); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon outside the service is still running")
	}
	if !f.Running() {
		t.Error("the service didn't start")
	}
}

func TestInitOpHandsOverAfterAnswering(t *testing.T) {
	newSandbox(t)
	withService(t, "linux")
	ended := make(chan struct{})
	endDaemon.Lock()
	endDaemon.fn = func() { close(ended) }
	endDaemon.Unlock()
	t.Cleanup(func() {
		endDaemon.Lock()
		endDaemon.fn = nil
		endDaemon.Unlock()
	})
	res, err := daemonInit(context.Background())
	if err != nil || !res.Done {
		t.Fatalf("%v: %+v", err, res)
	}
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon didn't make way for the service")
	}
}

func TestDoctorReportsTheService(t *testing.T) {
	sessionsHere(t)
	newSandbox(t)
	f := withService(t, "linux")
	out, _, _ := execute(t, "", "doctor")
	if !strings.Contains(out, "not installed") || !strings.Contains(out, "acmd service install") {
		t.Errorf("before install:\n%s", out)
	}
	if _, err := f.Env().Install(context.Background(), testArgv0, service.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	out, _, _ = execute(t, "", "doctor")
	if !strings.Contains(out, "from boot (systemd)") {
		t.Errorf("installed:\n%s", out)
	}
}

func TestUnsupportedServiceCommands(t *testing.T) {
	newSandbox(t)
	for _, args := range [][]string{{"service", "install"}, {"service", "uninstall"}} {
		if _, _, err := execute(t, "", args...); err == nil || !strings.Contains(err.Error(), "plan9") {
			t.Errorf("%v: %v", args, err)
		}
	}
	if out, _, err := execute(t, "", "service", "status"); err != nil || !strings.Contains(out, "plan9") {
		t.Errorf("status: %v %s", err, out)
	}
	localService = func() (service.Env, error) { return service.Env{}, errors.New("no home") }
	t.Cleanup(func() {
		localService = func() (service.Env, error) { return servicetest.New("plan9", os.Getenv("HOME")).Env(), nil }
	})
	if _, _, err := execute(t, "", "service", "status"); err == nil {
		t.Error("status without a home")
	}
}

func TestAnAcmdOnOtherFoldersLeavesTheServiceAlone(t *testing.T) {
	shortPaths(t)
	f := withService(t, "linux")
	if _, err := f.Env().Install(context.Background(), "/bin/acmd", service.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	orig := onDefaultFolders
	onDefaultFolders = func() bool { return !paths.Overridden() }
	t.Cleanup(func() { onDefaultFolders = orig })
	before := len(f.Calls())

	// The service runs the default folders: this acmd's are others.
	out, err := run(t, "acmd", "daemon", "stop")
	if err != nil || !strings.Contains(out, "wasn't running") {
		t.Errorf("stop: %v %q", err, out)
	}
	if calls := f.Calls()[before:]; len(calls) != 0 {
		t.Errorf("stopping this acmd's daemon reached the service: %q", calls)
	}

	for _, k := range paths.Overrides {
		t.Setenv(k, "")
	}
	if _, ok := installedService(); !ok {
		t.Error("on the default folders, the service is this acmd's")
	}
}
