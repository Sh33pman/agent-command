package doctor

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/setup"
)

// fake is a temp home set up the way `acmd init` leaves it.
type fake struct {
	t        *testing.T
	home     string
	acmd     string
	env      map[string]string
	versions map[string]string // binary base name → --version output
	daemon   DaemonState
	plan     *setup.Plan
}

func newFake(t *testing.T) *fake {
	t.Helper()
	home := t.TempDir()
	f := &fake{t: t, home: home, env: map[string]string{"X_UNUSED": "unix:/tmp/kitty-acmd-1"},
		versions: map[string]string{"claude": "2.1.320 (Claude Code)\n"},
		daemon:   DaemonState{Running: true, Status: ipc.Status{Pid: 42, Sessions: 3, Events: 1}},
		plan:     &setup.Plan{}}
	bin := filepath.Join(home, "bin")
	f.acmd = filepath.Join(bin, "acmd")
	for _, name := range []string{"acmd", "claude", "code"} {
		f.write(filepath.Join(bin, name), "#!/bin/sh\n", 0o755)
	}
	p := f.paths()
	f.write(p.Setup(), `{"version":"0.7.0","at":"2026-10-31T10:00:00Z","acmd_path":"`+f.acmd+`","claude_dir":"`+
		filepath.Join(home, ".claude")+`"}`, 0o600)
	f.write(p.Env(), `{"PATH":"`+bin+`"}`, 0o600)
	cfg, err := config.Set(config.Template, "editor", "preset", "vscode")
	must(t, err)
	cfg, err = config.Set(cfg, "editor", "resolved", filepath.Join(bin, "code"))
	must(t, err)
	f.write(p.Config(), cfg, 0o600)
	settings, _, err := setup.AddHooks([]byte(`{"model":"opus"}`), f.acmd)
	must(t, err)
	f.write(filepath.Join(home, ".claude/settings.json"), string(settings), 0o600)
	must(t, os.Chmod(p.StateDir, 0o700))
	return f
}

func (f *fake) paths() paths.Paths {
	return paths.Paths{StateDir: filepath.Join(f.home, "state"), RuntimeDir: filepath.Join(f.home, "run"), ConfigDir: filepath.Join(f.home, "config")}
}

func (f *fake) write(path, content string, mode os.FileMode) {
	f.t.Helper()
	must(f.t, os.MkdirAll(filepath.Dir(path), 0o700))
	must(f.t, os.WriteFile(path, []byte(content), mode))
	must(f.t, os.Chmod(path, mode))
}

func (f *fake) Env() Env {
	bin := filepath.Join(f.home, "bin")
	return Env{
		Home: f.home, Paths: f.paths(), Version: "0.8.0", AcmdPath: f.acmd,
		Getenv: func(k string) string { return f.env[k] }, Read: os.ReadFile, Stat: os.Stat, Resolve: filepath.EvalSymlinks,
		LoginPath: bin,
		LookPath:  func(n string) (string, error) { return setup.LookPathIn(n, bin) },
		Output: func(_ context.Context, name string, _ ...string) ([]byte, error) {
			if v, ok := f.versions[filepath.Base(name)]; ok {
				return []byte(v), nil
			}
			return nil, errors.New("exit status 1")
		},
		HasBundle: func(string) bool { return false },
		Daemon:    func(context.Context) (DaemonState, error) { return f.daemon, nil },
		InitPlan:  func(context.Context) (*setup.Plan, error) { return f.plan, nil },
	}
}

func (f *fake) run() (*Report, string) {
	r := Run(context.Background(), f.Env())
	var b bytes.Buffer
	r.Write(&b)
	return r, b.String()
}

func TestHealthy(t *testing.T) {
	f := newFake(t)
	r, out := f.run()
	if r.Count(Warn) != 0 || r.Count(Fail) != 0 {
		t.Fatalf("healthy setup has problems:\n%s", out)
	}
	for _, want := range []string{
		"acmd\n  · version 0.8.0 at ~/bin/acmd\n  ✓ set up by acmd 0.7.0 on ",
		"✓ editor: VS Code (~/bin/code)", "✓ login-shell environment saved", "✓ setup is up to date",
		"Claude Code\n  ✓ claude 2.1.320 (~/bin/claude)\n  ✓ hooks registered for all 15 events",
		"Daemon\n  ✓ running (pid 42): 3 sessions, 1 event recorded", "Everything looks good.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "\nacmd\n")+strings.Count(out[:5], "acmd\n") != 1 {
		t.Errorf("the acmd section is split:\n%s", out)
	}
}

func TestNotSetUp(t *testing.T) {
	f := newFake(t)
	must(t, os.Remove(f.paths().Setup()))
	f.daemon = DaemonState{}
	r, out := f.run()
	for _, want := range []string{"✗ setup hasn't run yet\n      fix: acmd doctor --fix",
		"· not running (it starts with the first session or command)"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "editor") {
		t.Errorf("editor checked before setup:\n%s", out)
	}
	if r.Count(Fail) != 1 || r.Count(Warn) != 0 {
		t.Errorf("only the missing setup is a problem before init:\n%s", out)
	}
}

func TestProblems(t *testing.T) {
	f := newFake(t)
	home, p := f.home, f.paths()
	must(t, os.Chmod(p.StateDir, 0o755))

	// The binary hooks use is gone; this one lives elsewhere.
	must(t, os.Remove(f.acmd))
	other := filepath.Join(home, ".local/bin/acmd")
	f.write(other, "#!/bin/sh\n", 0o755)
	f.write(filepath.Join(home, "brew/acmd"), "#!/bin/sh\n", 0o755)
	f.write(filepath.Join(home, "bin/acmd"), "#!/bin/sh\n", 0o755) // reinstalled later: same path again…
	must(t, os.Remove(filepath.Join(home, "bin/acmd")))            // …and removed: keeps "no longer exists"
	env := f.Env()
	env.AcmdPath = other
	env.LoginPath = filepath.Join(home, ".local/bin") + ":" + filepath.Join(home, "brew") + ":relative"

	// Claude: old version, partial hooks, hooks disabled, managed hooks only, moved config dir.
	f.versions["claude"] = "2.0.9 (Claude Code)"
	settings, _, err := setup.AddHooks([]byte(`{"disableAllHooks": true}`), f.acmd)
	must(t, err)
	settings = bytes.Replace(settings, []byte(`"SessionEnd"`), []byte(`"SessionEndX"`), 1)
	f.write(filepath.Join(home, ".claude/settings.json"), string(settings), 0o600)
	managed := filepath.Join(home, "managed-settings.json")
	f.write(managed, `{"allowManagedHooksOnly": true}`, 0o644)
	orig := ManagedSettings
	ManagedSettings = []string{managed, filepath.Join(home, "missing.json")}
	t.Cleanup(func() { ManagedSettings = orig })

	// acmd: env.json too open, custom editor missing, daemon of another version, init would change things.
	f.write(p.Env(), `{"PATH":"x"}`, 0o644)
	cfg, err := config.Set(config.Template, "editor", "preset", "custom")
	must(t, err)
	cfg, err = config.Set(cfg, "editor", "command", []string{"nope", "{dir}"})
	must(t, err)
	f.write(p.Config(), cfg, 0o600)
	f.daemon = DaemonState{Running: true, Mismatch: "0.7.0"}
	f.plan = &setup.Plan{Changes: []setup.Change{{Path: filepath.Join(home, ".claude/settings.json"), New: []byte("x")}}}

	r := Run(context.Background(), env)
	var b bytes.Buffer
	r.Write(&b)
	out := b.String()
	for _, want := range []string{
		"✗ hooks use ~/bin/acmd, which no longer exists",
		"! several acmd installs on your PATH (the first wins): ~/.local/bin/acmd, ~/brew/acmd",
		"✗ editor command \"nope\" not found",
		"! ~/state/env.json is readable by others (-rw-r--r--); it holds your environment\n      fix: chmod 600 ~/state/env.json",
		"! acmd init would update ~/.claude/settings.json",
		"✗ claude 2.0.9 is older than 2.1.314, the oldest acmd is tested with\n      fix: upgrade claude",
		"✗ acmd hooks for only 14 of 15 events",
		"✗ hooks run ~/bin/acmd, which doesn't exist",
		"✗ disableAllHooks is on in your settings",
		"✗ managed settings allow only managed hooks",
		"! ~/state is accessible by others (-rwxr-xr-x)\n      fix: chmod 700 ~/state",
		"! running acmd 0.7.0 (this is 0.8.0)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	if !strings.HasSuffix(out, "7 problems, 5 warnings.\n") {
		t.Errorf("summary:\n%s", out)
	}

	// CLAUDE_CONFIG_DIR set to the default is the same as unset.
	f.env["CLAUDE_CONFIG_DIR"] = filepath.Join(home, ".claude") + "/"
	if _, out = f.run(); strings.Contains(out, "CLAUDE_CONFIG_DIR") {
		t.Errorf("default CLAUDE_CONFIG_DIR reported:\n%s", out)
	}
	// CLAUDE_CONFIG_DIR now points elsewhere than where the hooks went.
	f.env["CLAUDE_CONFIG_DIR"] = filepath.Join(home, "other-claude")
	_, out = f.run()
	if !strings.Contains(out, "✗ hooks are in ~/.claude, but Claude now reads ~/other-claude (CLAUDE_CONFIG_DIR)") {
		t.Errorf("moved CLAUDE_CONFIG_DIR not reported:\n%s", out)
	}
	// Before setup, a custom CLAUDE_CONFIG_DIR is worth knowing about.
	must(t, os.Remove(p.Setup()))
	if _, out = f.run(); !strings.Contains(out, "· CLAUDE_CONFIG_DIR is set: hooks go into ~/other-claude") {
		t.Errorf("custom CLAUDE_CONFIG_DIR not noted:\n%s", out)
	}
}

func TestDaemonAndPlanErrors(t *testing.T) {
	f := newFake(t)
	env := f.Env()
	env.Daemon = func(context.Context) (DaemonState, error) { return DaemonState{}, errors.New("permission denied") }
	env.InitPlan = func(context.Context) (*setup.Plan, error) { return nil, errors.New("boom") }
	r := Run(context.Background(), env)
	var b bytes.Buffer
	r.Write(&b)
	for _, want := range []string{"✗ can't talk to the daemon: permission denied", "! couldn't check whether setup is current: boom"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, b.String())
		}
	}
}

func TestVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{{"2.1.314", "2.1.314", 0}, {"2.1.320", "2.1.314", 1}, {"2.1.99", "2.1.314", -1}, {"0.49", "0.49.0", 0}, {"1.0", "0.49.2", 1}} {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%s, %s) = %d", c.a, c.b, got)
		}
	}
	if v := ParseClaudeVersion("2.1.314 (Claude Code)\n"); v != "2.1.314" {
		t.Errorf("claude: %q", v)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
