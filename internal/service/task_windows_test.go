package service_test

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/service"
)

// TestRealLogonTask registers the logon task with the real Task Scheduler,
// runs it and removes it. It runs in CI (Windows runners are throwaway), or
// with ACMD_TEST_TASK=1. The daemon is a copy of this test binary named
// acmd.exe; the launcher is acmdw, built as the release builds it.
func TestRealLogonTask(t *testing.T) {
	if os.Getenv("CI") != "true" && os.Getenv("ACMD_TEST_TASK") != "1" {
		t.Skip("registers a real logon task: set ACMD_TEST_TASK=1 to run it here")
	}
	ctx := context.Background()
	dir := t.TempDir()
	acmd := filepath.Join(dir, "acmd.exe")
	copyFile(t, os.Args[0], acmd)
	build := exec.Command("go", "build", "-ldflags", "-H=windowsgui", "-o", filepath.Join(dir, service.Launcher), "../../cmd/acmdw")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the launcher: %v\n%s", err, out)
	}

	e, err := service.Local()
	must(t, err)
	e.LocalAppData = t.TempDir() // the task's XML and pid file, not the runner's own
	t.Cleanup(func() { _, _ = e.Uninstall(context.WithoutCancel(ctx)) })
	ch, err := e.Install(ctx, acmd, service.InstallOptions{})
	if err != nil || !ch.Started {
		t.Fatalf("install: %+v %v", ch, err)
	}
	if out, err := exec.Command("schtasks", "/Query", "/TN", service.TaskName).CombinedOutput(); err != nil {
		t.Fatalf("the task isn't registered: %v\n%s", err, out)
	}

	var started []byte
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if started, _ = os.ReadFile(filepath.Join(dir, "started")); len(started) > 0 && e.Running(ctx) {
			break
		}
	}
	if string(started) != "1 daemon" {
		t.Fatalf("the daemon got %q, want ACMD_SERVICE=1 and daemon (log: %s)", started, readLog(e))
	}
	st := e.Status(ctx)
	if !st.Installed || !st.Running || st.Binary != acmd {
		t.Errorf("status = %+v", st)
	}

	pid := e.Pid(ctx)
	if removed, err := e.Uninstall(ctx); err != nil || !removed {
		t.Fatalf("uninstall: %v %v", removed, err)
	}
	if err := exec.Command("schtasks", "/Query", "/TN", service.TaskName).Run(); err == nil {
		t.Error("the task is still registered")
	}
	deadline := time.Now().Add(10 * time.Second)
	for e.Alive(pid) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if e.Alive(pid) {
		t.Error("the daemon still runs")
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	in, err := os.Open(from)
	must(t, err)
	defer in.Close()
	out, err := os.Create(to)
	must(t, err)
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	must(t, out.Close())
}

func readLog(e service.Env) string {
	b, _ := os.ReadFile(filepath.Join(filepath.Dir(e.PidFile()), "daemon.log"))
	return string(b)
}
