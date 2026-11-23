package service_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/service"
	"github.com/brettmostert/agent-command/internal/service/servicetest"
)

const bin = "/home/linuxbrew/.linuxbrew/bin/acmd"

func TestUnitContents(t *testing.T) {
	f := servicetest.New("linux", t.TempDir())
	unit := string(f.Env().Definition(bin))
	for _, want := range []string{
		"ExecStart=" + bin + " daemon\n",
		"Environment=ACMD_SERVICE=1\n",
		// Session hosts must outlive a restart.
		"KillMode=process\n",
		"Restart=always\n",
		"WantedBy=default.target\n",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit lacks %q:\n%s", want, unit)
		}
	}
	if got := service.Binary([]byte(unit)); got != bin {
		t.Errorf("Binary = %q", got)
	}
	spaced := f.Env().Definition("/opt/my tools/acmd")
	if !strings.Contains(string(spaced), `ExecStart="/opt/my tools/acmd" daemon`) {
		t.Errorf("a path with a space isn't quoted:\n%s", spaced)
	}
	if got := service.Binary(spaced); got != "/opt/my tools/acmd" {
		t.Errorf("Binary of a quoted path = %q", got)
	}
}

func TestPlistContents(t *testing.T) {
	f := servicetest.New("darwin", t.TempDir())
	app := "/Applications/Agent Command.app/Contents/MacOS/agent-command"
	plist := string(f.Env().Definition(app))
	for _, want := range []string{
		"<string>" + service.Label + "</string>",
		"<string>" + app + "</string>\n\t\t<string>daemon</string>",
		"<key>ACMD_SERVICE</key>\n\t\t<string>1</string>",
		"<key>RunAtLoad</key>\n\t<true/>",
		"<key>KeepAlive</key>\n\t<true/>",
		// Session hosts must outlive a restart.
		"<key>AbandonProcessGroup</key>\n\t<true/>",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist lacks %q:\n%s", want, plist)
		}
	}
	if got := service.Binary([]byte(plist)); got != app {
		t.Errorf("Binary = %q", got)
	}
	if got := service.Binary(f.Env().Definition("/x/a&b")); got != "/x/a&b" {
		t.Errorf("Binary of an escaped path = %q", got)
	}
	if f.Env().File() != filepath.Join(f.Home, "Library/LaunchAgents/com.brettmostert.agent-command.daemon.plist") {
		t.Errorf("File = %s", f.Env().File())
	}
}

func TestSystemdInstallUninstall(t *testing.T) {
	ctx := context.Background()
	f := servicetest.New("linux", t.TempDir())
	e := f.Env()
	if e.Installed() {
		t.Fatal("installed before install")
	}
	ch, err := e.Install(ctx, bin, service.InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !ch.Wrote || !ch.Started || len(ch.Notes) != 0 {
		t.Errorf("first install: %+v", ch)
	}
	if !f.Enabled || !f.Linger || !f.Running() || !e.Installed() {
		t.Errorf("after install: enabled %v, linger %v, running %v", f.Enabled, f.Linger, f.Running())
	}
	b, err := os.ReadFile(filepath.Join(f.Home, ".config/systemd/user/agent-command.service"))
	if err != nil || service.Binary(b) != bin {
		t.Errorf("unit file: %v %q", err, b)
	}
	s := e.Status(ctx)
	if !s.Supported || !s.Installed || !s.Running || s.Binary != bin || s.Linger == nil || !*s.Linger || s.Manager != service.Systemd {
		t.Errorf("status: %+v", s)
	}

	// A rerun with nothing to change leaves the running daemon alone.
	pid := f.Pid
	if ch, err := e.Install(ctx, bin, service.InstallOptions{}); err != nil || ch.Wrote || ch.Started || f.Pid != pid {
		t.Errorf("rerun: %+v %v, pid %d → %d", ch, err, pid, f.Pid)
	}
	// A new binary restarts it.
	if ch, err := e.Install(ctx, "/usr/local/bin/acmd", service.InstallOptions{}); err != nil || !ch.Wrote || !ch.Started || f.Pid == pid {
		t.Errorf("new binary: %+v %v", ch, err)
	}
	// Stopped, a rerun starts it.
	if err := e.Stop(ctx); err != nil || f.Running() {
		t.Fatalf("stop: %v", err)
	}
	if ch, err := e.Install(ctx, "/usr/local/bin/acmd", service.InstallOptions{}); err != nil || !ch.Started || !f.Running() {
		t.Errorf("rerun when stopped: %+v %v", ch, err)
	}

	removed, err := e.Uninstall(ctx)
	if err != nil || !removed || f.Enabled || f.Running() || e.Installed() {
		t.Errorf("uninstall: %v %v, enabled %v", removed, err, f.Enabled)
	}
	if removed, err := e.Uninstall(ctx); err != nil || removed {
		t.Errorf("uninstall again: %v %v", removed, err)
	}
}

func TestSystemdLingerRefused(t *testing.T) {
	f := servicetest.New("linux", t.TempDir())
	f.LingerFails = true
	ch, err := f.Env().Install(context.Background(), bin, service.InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ch.Notes) != 1 || !strings.Contains(ch.Notes[0], "sudo loginctl enable-linger tester") {
		t.Errorf("notes: %q", ch.Notes)
	}
	if s := f.Env().Status(context.Background()); s.Linger == nil || *s.Linger {
		t.Errorf("status linger: %+v", s)
	}
}

func TestNoStartOnlyEnables(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		f := servicetest.New(goos, t.TempDir())
		ch, err := f.Env().Install(context.Background(), bin, service.InstallOptions{NoStart: true})
		if err != nil || !ch.Wrote || ch.Started || f.Running() || f.Loaded {
			t.Errorf("%s: %+v %v, running %v", goos, ch, err, f.Running())
		}
	}
}

func TestSelfRestartIsDetached(t *testing.T) {
	ctx := context.Background()
	for _, goos := range []string{"linux", "darwin"} {
		f := servicetest.New(goos, t.TempDir())
		e := f.Env()
		if _, err := e.Install(ctx, bin, service.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
		pid := f.Pid
		// The service's own daemon, with a definition to change: it must answer
		// before it's restarted.
		ch, err := e.Install(ctx, "/new/acmd", service.InstallOptions{Self: true})
		if err != nil || !ch.Wrote || ch.Started || f.Pid != pid {
			t.Errorf("%s: %+v %v", goos, ch, err)
		}
		if d := f.Detached(); len(d) != 1 || !strings.HasPrefix(d[0], "sleep 1; ") {
			t.Errorf("%s: detached %q", goos, d)
		}
	}
}

func TestLaunchdInstallUninstall(t *testing.T) {
	ctx := context.Background()
	f := servicetest.New("darwin", t.TempDir())
	e := f.Env()
	ch, err := e.Install(ctx, bin, service.InstallOptions{})
	if err != nil || !ch.Wrote || !ch.Started || !f.Loaded || !f.Running() {
		t.Fatalf("install: %+v %v", ch, err)
	}
	if s := e.Status(ctx); !s.Running || s.Pid != f.Pid || s.Linger != nil || s.Manager != service.Launchd {
		t.Errorf("status: %+v", s)
	}
	pid := f.Pid
	if ch, err := e.Install(ctx, bin, service.InstallOptions{}); err != nil || ch.Started || f.Pid != pid {
		t.Errorf("rerun: %+v %v", ch, err)
	}
	// launchd reads a changed plist only when it loads it.
	if _, err := e.Install(ctx, "/new/acmd", service.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	calls := f.Calls()
	if i := slices.Index(calls, "launchctl bootout gui/501/"+service.Label); i < 0 ||
		!strings.HasPrefix(calls[len(calls)-1], "launchctl bootstrap gui/501 ") {
		t.Errorf("changed plist isn't reloaded: %q", calls)
	}
	if err := e.Restart(ctx); err != nil || f.Pid == pid {
		t.Errorf("restart: %v", err)
	}
	if err := e.Stop(ctx); err != nil || f.Loaded || f.Running() {
		t.Errorf("stop: %v", err)
	}
	if err := e.Start(ctx); err != nil || !f.Running() {
		t.Errorf("start after stop: %v", err)
	}
	if removed, err := e.Uninstall(ctx); err != nil || !removed || f.Loaded || e.Installed() {
		t.Errorf("uninstall: %v %v", removed, err)
	}
}

func TestLaunchdWithNoOneLoggedIn(t *testing.T) {
	f := servicetest.New("darwin", t.TempDir())
	f.BootstrapFails = true
	_, err := f.Env().Install(context.Background(), bin, service.InstallOptions{})
	if err == nil || !strings.Contains(err.Error(), "it starts at your next login") {
		t.Errorf("err = %v", err)
	}
	if !f.Env().Installed() {
		t.Error("the plist should be in place for the next login")
	}
}

func TestUnsupported(t *testing.T) {
	ctx := context.Background()
	wsl := servicetest.New("linux", t.TempDir())
	wsl.NoSystemd, wsl.WSL = true, true
	_, err := wsl.Env().Install(ctx, bin, service.InstallOptions{})
	if !errors.Is(err, service.ErrUnsupported) {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{"/etc/wsl.conf", "[boot]", "systemd=true", "wsl --shutdown", "starts when something needs it"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("WSL message lacks %q: %v", want, err)
		}
	}
	if s := wsl.Env().Status(ctx); s.Supported || !strings.HasPrefix(s.Note, "WSL isn't running systemd") {
		t.Errorf("status: %+v", s)
	}
	if wsl.Env().Installed() {
		t.Error("installed without systemd")
	}
	if len(wsl.Calls()) != 0 {
		t.Errorf("ran %q", wsl.Calls())
	}

	plain := servicetest.New("linux", t.TempDir())
	plain.NoSystemd = true
	if _, err := plain.Env().Manager(); !errors.Is(err, service.ErrUnsupported) || strings.Contains(err.Error(), "wsl") {
		t.Errorf("Linux without systemd: %v", err)
	}
	for _, goos := range []string{"windows", "freebsd"} {
		e := servicetest.New(goos, t.TempDir()).Env()
		if _, err := e.Uninstall(ctx); !errors.Is(err, service.ErrUnsupported) {
			t.Errorf("%s: %v", goos, err)
		}
		if e.Start(ctx) == nil || e.Stop(ctx) == nil || e.Restart(ctx) == nil || e.Running(ctx) {
			t.Errorf("%s: commands should fail", goos)
		}
	}
}

func TestLocal(t *testing.T) {
	e, err := service.Local()
	if err != nil || e.Home == "" || e.Run == nil {
		t.Fatalf("Local: %+v %v", e, err)
	}
}
