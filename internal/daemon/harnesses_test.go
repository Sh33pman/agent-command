package daemon

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/brettmostert/agent-command/internal/claudecli"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/errcode"
	"github.com/brettmostert/agent-command/internal/ipc"
)

func harnessesOf(t *testing.T, c *ipc.Conn) ipc.Harnesses {
	t.Helper()
	var hs ipc.Harnesses
	must(t, c.Call(ipc.OpHarnesses, nil, &hs))
	return hs
}

func strp(s string) *string { return &s }

// Harnesses are added, renamed, changed and removed through ops, which
// check the name and the kind; the snapshot lists them.
func TestHarnessOps(t *testing.T) {
	p := testPaths(t)
	home := t.TempDir()
	var told [][]core.Harness
	var mu sync.Mutex
	start(t, p, func(c *Config) {
		c.Home = home
		c.HarnessesChanged = func(_ context.Context, hs []core.Harness) {
			mu.Lock()
			defer mu.Unlock()
			told = append(told, hs)
		}
	})
	c := dial(t, p)

	var work ipc.Harness
	must(t, c.Call(ipc.OpHarnessAdd, ipc.HarnessSave{Kind: core.HarnessClaude, Name: "My Work"}, &work))
	if work.ID == "" || work.Name != "My Work" || work.ConfigDir != "~/.claude-my_work" || work.Folder != filepath.Join(home, ".claude-my_work") ||
		work.Type != ipc.EntityHarness || work.Order != 3 || !work.Installed {
		t.Errorf("added = %+v", work)
	}
	for what, r := range map[string]ipc.HarnessSave{
		"a digit":        {Kind: core.HarnessClaude, Name: "Work2"},
		"no name":        {Kind: core.HarnessClaude, Name: " "},
		"taken":          {Kind: core.HarnessClaude, Name: "my_work"},
		"taken by agy":   {Kind: core.HarnessAntigravity, Name: "CLAUDE"},
		"no kind":        {Name: "Other"},
		"a terminal":     {Kind: core.HarnessTerminal, Name: "Shell"},
		"agy has no dir": {Kind: core.HarnessAntigravity, Name: "Gem", ConfigDir: strp("/x")},
	} {
		if err := c.Call(ipc.OpHarnessAdd, r, nil); codeOf(err) != errcode.Invalid {
			t.Errorf("%s: %v", what, err)
		}
	}

	// A rename keeps its folder (its sign-in is there) and its id.
	var renamed ipc.Harness
	must(t, c.Call(ipc.OpHarnessUpdate, ipc.HarnessSave{ID: work.ID, Name: "Job", Args: &[]string{"--model", " ", "opus"}}, &renamed))
	if renamed.ID != work.ID || renamed.Name != "Job" || renamed.ConfigDir != "~/.claude-my_work" || !slices.Equal(renamed.Args, []string{"--model", "opus"}) {
		t.Errorf("renamed = %+v", renamed)
	}
	if err := c.Call(ipc.OpHarnessUpdate, ipc.HarnessSave{ID: work.ID, Kind: core.HarnessAntigravity}, nil); codeOf(err) != errcode.Invalid {
		t.Errorf("a kind change: %v", err)
	}
	if err := c.Call(ipc.OpHarnessUpdate, ipc.HarnessSave{ID: "nope", Name: "X"}, nil); codeOf(err) != errcode.NotFound {
		t.Errorf("no such harness: %v", err)
	}
	var std ipc.Harness
	must(t, c.Call(ipc.OpHarnessUpdate, ipc.HarnessSave{ID: work.ID, ConfigDir: strp("")}, &std))
	if std.ConfigDir != "" || std.Name != "Job" {
		t.Errorf("Claude's standard folder: %+v", std)
	}

	snap := snapshot(t, c)
	if len(snap.Harnesses) != 3 || snap.Harnesses[2].ID != work.ID {
		t.Errorf("snapshot = %+v", snap.Harnesses)
	}
	mu.Lock()
	if len(told) != 3 || len(told[0]) != 3 {
		t.Errorf("told %d times", len(told))
	}
	mu.Unlock()

	must(t, c.Call(ipc.OpHarnessRemove, ipc.HarnessRef{ID: work.ID}, nil))
	if hs := harnessesOf(t, c); len(hs.Harnesses) != 2 || hs.Default != hs.Harnesses[0].ID || !hs.Terminal {
		t.Errorf("after remove = %+v", hs)
	}
	if err := c.Call(ipc.OpHarnessRemove, ipc.HarnessRef{ID: work.ID}, nil); codeOf(err) != errcode.NotFound {
		t.Errorf("removed twice: %v", err)
	}
	if err := c.Call(ipc.OpHarnessAdd, "x", nil); codeOf(err) != errcode.BadRequest {
		t.Errorf("a request that isn't one: %v", err)
	}
}

// A session runs in its harness: its program, arguments and folder go to its
// host. Removing the harness leaves the session, and resuming it uses its
// kind's default harness. A harness a job runs is never removed.
func TestSessionsRunInTheirHarness(t *testing.T) {
	d, fh, _, _ := jobDaemon(t)
	ctx := context.Background()
	d.cfg.Home = t.TempDir()
	out, err := d.saveHarness(ctx, []byte(`{"kind":"claude","name":"Work","command":"/bin/claude-work","args":["--x"]}`), true)
	must(t, err)
	work := out.(ipc.Harness)

	s, err := d.hostSession(ctx, ipc.NewRequest{Cwd: t.TempDir(), HarnessID: work.ID})
	must(t, err)
	r := fh.requests()
	if s.HarnessID != work.ID || s.HarnessKind != core.HarnessClaude || len(r) != 1 || r[0].Harness == nil ||
		r[0].Harness.Command != "/bin/claude-work" || r[0].Harness.ConfigDir != "~/.claude-work" || r[0].Kind != core.HarnessClaude {
		t.Errorf("session %+v, request %+v", s, r)
	}
	// By kind: its first harness. A terminal: none.
	agy, err := d.hostSession(ctx, ipc.NewRequest{Cwd: t.TempDir(), HarnessKind: core.HarnessAntigravity})
	must(t, err)
	if agy.HarnessID != kindID(t, d, core.HarnessAntigravity) {
		t.Errorf("by kind: %+v", agy)
	}
	term, err := d.hostSession(ctx, ipc.NewRequest{Cwd: t.TempDir(), HarnessKind: core.HarnessTerminal})
	must(t, err)
	if term.HarnessID != "" || term.HarnessKind != core.HarnessTerminal || fh.requests()[2].Harness != nil {
		t.Errorf("terminal: %+v", term)
	}
	if _, err := d.hostSession(ctx, ipc.NewRequest{Cwd: "/", HarnessID: "nope"}); resultOf("", err).Code != errcode.NotFound {
		t.Errorf("no such harness: %v", err)
	}
	if _, err := d.hostSession(ctx, ipc.NewRequest{Cwd: "/", HarnessID: work.ID, HarnessKind: core.HarnessAntigravity}); resultOf("", err).Code != errcode.Invalid {
		t.Errorf("the wrong kind: %v", err)
	}

	job := folderJob(t.TempDir())
	job.HarnessID = work.ID
	saved := mustSave(t, d, job)
	if _, err := d.removeHarness(ctx, []byte(`{"id":"`+work.ID+`"}`)); resultOf("", err).Code != errcode.Conflict || !strings.Contains(err.Error(), saved.Name) {
		t.Errorf("a job's harness: %v", err)
	}
	must(t, d.store.DeleteJob(ctx, saved.ID))
	if _, err := d.removeHarness(ctx, []byte(`{"id":"`+work.ID+`"}`)); err != nil {
		t.Fatal(err)
	}
	kept, err := d.store.Session(ctx, s.ID)
	must(t, err)
	h, err := d.harnessForSession(ctx, kept)
	if err != nil || kept.HarnessID != work.ID || h.ID != kindID(t, d, core.HarnessClaude) {
		t.Errorf("a removed harness's session: %+v, now %+v %v", kept, h, err)
	}
}

type folderAgents struct {
	dir    string
	agents []claudecli.Agent
}

func (f folderAgents) Agents(context.Context) ([]claudecli.Agent, error) { return f.agents, nil }
func (f folderAgents) Stop(context.Context, string) error                { return nil }
func (f folderAgents) Remove(context.Context, string) error              { return nil }

// Each Claude harness's history comes from its own folder only, and Claude's
// background sessions are listed in every folder.
func TestHarnessFoldersHistoryAndAgents(t *testing.T) {
	p := testPaths(t)
	home := t.TempDir()
	std, work := t.TempDir(), filepath.Join(home, ".claude-work")
	must(t, os.MkdirAll(work, 0o700))
	writeConversation(t, std, "11111111-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "/w/std", "standard")
	writeConversation(t, work, "22222222-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "/w/work", "work")
	pid, idle := 1, "idle"
	start(t, p, func(c *Config) {
		c.Home, c.ClaudeDir, c.Hosts = home, std, &fakeHosts{paths: p}
		c.Agents = folderAgents{agents: []claudecli.Agent{{SessionID: "11111111-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Kind: "interactive", Pid: &pid, Status: &idle}}}
		c.ClaudeFor = func(h core.Harness) ClaudeCLI {
			return folderAgents{dir: h.ConfigDir, agents: []claudecli.Agent{{SessionID: "22222222-bbbb-4bbb-8bbb-bbbbbbbbbbbb", Kind: "interactive", Pid: &pid, Status: &idle}}}
		}
	})
	c := dial(t, p)
	var work1 ipc.Harness
	must(t, c.Call(ipc.OpHarnessAdd, ipc.HarnessSave{Kind: core.HarnessClaude, Name: "Work"}, &work1))
	must(t, c.Call(ipc.OpHarnessAdd, ipc.HarnessSave{Kind: core.HarnessClaude, Name: "Same", ConfigDir: strp("~/.claude-work")}, nil))

	list := conversationsOf(t, c)
	byHarness := map[string]string{}
	for _, x := range list {
		byHarness[x.Title] = x.HarnessID
		if !x.Running {
			t.Errorf("%s isn't running: each folder's agents are listed", x.Title)
		}
	}
	claude := harnessesOf(t, c).Harnesses[0].ID
	if len(list) != 2 || byHarness["standard"] != claude || byHarness["work"] != work1.ID {
		t.Errorf("conversations = %+v", list)
	}
	s := newSess(t, c, ipc.NewRequest{HarnessID: work1.ID, Resume: "22222222"})
	if s.HarnessID != work1.ID || s.Cwd != "/w/work" {
		t.Errorf("resumed in its harness: %+v", s)
	}
	if err := c.Call(ipc.OpNew, ipc.NewRequest{Resume: "22222222"}, nil); err == nil {
		t.Error("the standard folder's history has no work conversation")
	}
}

// What config.toml had moves into the harnesses, once.
func TestHarnessSettingsMoveOutOfConfig(t *testing.T) {
	p := settingsPaths(t)
	must(t, os.MkdirAll(p.ConfigDir, 0o700))
	must(t, os.WriteFile(p.Config(), []byte("# Absolute path of the claude binary.\nclaude_command = \"/opt/homebrew/bin/claude\"\nclaude_args = [\"--model\", \"opus\"]\nagy_command = \"agy\"\nagy_args = [\"--mode\", \"plan\"]\ndefault_harness = \"antigravity\"\n\n[notify]\ncooldown = \"1m\"\n"), 0o600))
	start(t, p, withConfig(p))
	c := dial(t, p)
	hs := harnessesOf(t, c)
	claude, agy := hs.Harnesses[0], hs.Harnesses[1]
	if claude.Command != "/opt/homebrew/bin/claude" || !slices.Equal(claude.Args, []string{"--model", "opus"}) ||
		agy.Command != "" || !slices.Equal(agy.Args, []string{"--mode", "plan"}) || hs.Default != agy.ID {
		t.Errorf("harnesses = %+v", hs)
	}
	text, err := os.ReadFile(p.Config())
	must(t, err)
	if strings.Contains(string(text), "claude_") || strings.Contains(string(text), "agy_") || strings.Contains(string(text), "Absolute path") ||
		!strings.Contains(string(text), `default_harness = "`+agy.ID+`"`) || !strings.Contains(string(text), `cooldown = "1m"`) {
		t.Errorf("config.toml:\n%s", text)
	}
}
