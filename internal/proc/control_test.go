package proc

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestMain lets the test binary stand in for a long-running child.
func TestMain(m *testing.M) {
	if os.Getenv("ACMD_PROC_TEST_SLEEP") == "1" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestDetachedChildIsAliveUntilTerminated(t *testing.T) {
	cmd := exec.Command(os.Args[0]) // #nosec G204 -- the test binary
	cmd.Env = append(os.Environ(), "ACMD_PROC_TEST_SLEEP=1")
	Detach(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	waited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(waited) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-waited })

	if !Alive(pid) {
		t.Fatal("a running child isn't alive")
	}
	if err := Terminate(pid); err != nil {
		t.Fatal(err)
	}
	select {
	case <-waited:
	case <-time.After(10 * time.Second):
		t.Fatal("the child didn't stop")
	}
	if Alive(pid) {
		t.Error("a child that exited is still alive")
	}
}

func TestAliveIsFalseForNoProcess(t *testing.T) {
	if Alive(0x3ffffff0) {
		t.Error("a pid with no process is alive")
	}
}
