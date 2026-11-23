package service_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/proc"
	"github.com/brettmostert/agent-command/internal/service"
)

// TestMain lets the test binary stand in for acmd: it records the environment
// it was started with, then waits a moment, as a daemon would run on.
func TestMain(m *testing.M) {
	// A copy named acmd.exe is the daemon a real logon task starts
	// (TestRealLogonTask): it says how it was started, next to itself.
	if strings.EqualFold(filepath.Base(os.Args[0]), "acmd.exe") {
		_ = os.WriteFile(filepath.Join(filepath.Dir(os.Args[0]), "started"),
			[]byte(os.Getenv(service.EnvVar)+" "+strings.Join(os.Args[1:], " ")), 0o600)
		time.Sleep(2 * time.Minute)
		os.Exit(0)
	}
	if out := os.Getenv("ACMD_LAUNCH_TEST_OUT"); out != "" {
		_ = os.WriteFile(out, []byte(os.Getenv(service.EnvVar)+" "+strings.Join(os.Args[1:], " ")), 0o600)
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestLaunchStartsTheDaemonAndRecordsItsPid(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	t.Setenv("ACMD_LAUNCH_TEST_OUT", out)
	pidFile := filepath.Join(dir, "service.pid")
	if err := service.Launch([]string{pidFile, os.Args[0], "daemon"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || !proc.Alive(pid) {
		t.Fatalf("pid file %q: %v, alive %v", b, err, proc.Alive(pid))
	}
	t.Cleanup(func() { _ = proc.Terminate(pid) })
	var got []byte
	for deadline := time.Now().Add(10 * time.Second); len(got) == 0 && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		got, _ = os.ReadFile(out)
	}
	if string(got) != "1 daemon" {
		t.Errorf("the daemon got %q, want ACMD_SERVICE=1 and daemon", got)
	}
}

func TestLaunchFailsAndSaysWhy(t *testing.T) {
	dir := t.TempDir()
	if err := service.Launch([]string{"only"}); err == nil {
		t.Error("launched with no acmd")
	}
	args := []string{filepath.Join(dir, "service.pid"), filepath.Join(dir, "no-acmd")}
	err := service.Launch(args)
	if err == nil {
		t.Fatal("launched an acmd that isn't there")
	}
	service.LaunchFailed(args, err)
	if b, _ := os.ReadFile(filepath.Join(dir, "daemon.log")); !strings.Contains(string(b), "acmdw: starting") {
		t.Errorf("daemon.log = %q", b)
	}
}
