package claudecli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "claudecli", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// fake records calls and answers from a table keyed by the first argument.
type fake struct {
	calls   [][]string
	dirs    []string
	envs    [][]string
	answers map[string]answer
}

type answer struct {
	out []byte
	err error
}

func (f *fake) run(_ context.Context, dir string, env []string, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	f.dirs = append(f.dirs, dir)
	f.envs = append(f.envs, env)
	a := f.answers[args[0]]
	return a.out, a.err
}

func newFake(answers map[string]answer) (*CLI, *fake) {
	f := &fake{answers: answers}
	return &CLI{Path: "/u/.local/bin/claude", Env: []string{"PATH=/bin", "CLAUDECODE=1", "CLAUDE_CODE_CHILD_SESSION=1"}, Run: f.run}, f
}

func TestParseAgentsRecorded(t *testing.T) {
	agents, err := ParseAgents(fixture(t, "agents-mixed.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 4 {
		t.Fatalf("got %d agents", len(agents))
	}
	idle, waiting, stopped, inter := agents[0], agents[1], agents[2], agents[3]
	if idle.ID != "9df2c810" || !idle.Background() || idle.Stopped() || idle.StatusString() != "idle" || *idle.Pid != 63434 || idle.Name != "exact word reply hello" {
		t.Errorf("idle = %+v", idle)
	}
	if waiting.StatusString() != "waiting" {
		t.Errorf("waiting = %+v", waiting)
	}
	if !stopped.Stopped() || stopped.StatusString() != "stopped" || stopped.Pid != nil {
		t.Errorf("stopped (pid/status null, M0 C3) = %+v", stopped)
	}
	if inter.Background() || inter.ID != "" || inter.Kind != "interactive" || inter.Cwd != "/work/other" {
		t.Errorf("interactive (id null, M0 C6) = %+v", inter)
	}

	live, err := ParseAgents(fixture(t, "agents-interactive.json"))
	if err != nil || len(live) != 1 || live[0].ID != "" || live[0].StatusString() != "busy" || live[0].StartedAt != 1793364942665 {
		t.Errorf("live recorded output: %+v %v", live, err)
	}
}

func TestParseAgentsLenient(t *testing.T) {
	agents, err := ParseAgents([]byte(`[{"id":"abcdef12","pid":"42","sessionId":"s","kind":"background","status":"busy","startedAt":"1"}]`))
	if err != nil || len(agents) != 1 || *agents[0].Pid != 42 || agents[0].StartedAt != 1 {
		t.Errorf("string pid/startedAt: %+v %v", agents, err)
	}
	if a, err := ParseAgents([]byte("  \n")); err != nil || a != nil {
		t.Errorf("empty output: %v %v", a, err)
	}
	if _, err := ParseAgents([]byte("not json")); err == nil {
		t.Error("expected error")
	}
	var bad Agent
	if err := bad.UnmarshalJSON([]byte(`[]`)); err == nil {
		t.Error("expected error for non-object")
	}
	z := 0
	if (Agent{Pid: &z}).Stopped() != true {
		t.Error("pid 0 is stopped")
	}
}

// The env scrub (M0 C5): never pass Claude's session variables to a new claude.
func TestRunScrubsClaudeEnv(t *testing.T) {
	c, f := newFake(map[string]answer{"stop": {}})
	if err := c.Stop(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	for _, kv := range f.envs[0] {
		if strings.HasPrefix(kv, "CLAUDE") {
			t.Errorf("env leaked %s", kv)
		}
	}
}

func TestStopRemoveAgents(t *testing.T) {
	c, f := newFake(map[string]answer{
		"stop":   {out: []byte("stopped 77d411e3\n")},
		"rm":     {err: errors.New("exit status 1"), out: []byte("no such session\n")},
		"agents": {out: fixture(t, "agents-mixed.json")},
	})
	ctx := context.Background()
	if err := c.Stop(ctx, "77d411e3"); err != nil {
		t.Errorf("Stop: %v", err)
	}
	if !reflect.DeepEqual(f.calls[0][1:], []string{"stop", "77d411e3"}) {
		t.Errorf("stop argv = %q", f.calls[0])
	}
	if err := c.Remove(ctx, "77d411e3"); err == nil || !strings.Contains(err.Error(), "no such session") {
		t.Errorf("Remove error should carry claude's message: %v", err)
	}
	agents, err := c.Agents(ctx)
	if err != nil || len(agents) != 4 {
		t.Errorf("Agents = %d, %v", len(agents), err)
	}
	if !reflect.DeepEqual(f.calls[len(f.calls)-1][1:], []string{"agents", "--json", "--all"}) {
		t.Errorf("agents argv = %q", f.calls[len(f.calls)-1])
	}
}

func TestRealRunner(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	c := New(sh, []string{"PATH=/usr/bin:/bin", "CLAUDECODE=1"})
	out, err := c.run(context.Background(), t.TempDir(), "-c", `echo "dir=$(pwd) claudecode=${CLAUDECODE:-unset}"`)
	if err != nil || !strings.Contains(string(out), "claudecode=unset") {
		t.Errorf("real runner: %q %v", out, err)
	}
	if _, err := c.run(context.Background(), "", "-c", "echo boom >&2; exit 3"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("failing command: %v", err)
	}
}
