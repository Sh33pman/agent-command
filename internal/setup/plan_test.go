package setup

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/config"
)

// fakeHome lays out a home with the fixture Claude settings (with user hooks).
func fakeHome(t *testing.T) (Input, map[string][]byte) {
	t.Helper()
	home := t.TempDir()
	orig := map[string][]byte{}
	put := func(rel string, data []byte) {
		p := filepath.Join(home, rel)
		must(t, os.MkdirAll(filepath.Dir(p), 0o755))
		must(t, os.WriteFile(p, data, 0o600))
		orig[rel] = data
	}
	put(".claude/settings.json", read(t, "claude-settings-with-user-hooks.json"))
	state := filepath.Join(home, ".local/state/agent-command")
	must(t, os.MkdirAll(state, 0o700))
	return Input{
		Home:       home,
		ConfigFile: filepath.Join(home, ".config/agent-command/config.toml"),
		EnvFile:    filepath.Join(state, "env.json"),
		AcmdPath:   "/opt/homebrew/bin/acmd",
		ClaudeDir:  filepath.Join(home, ".claude"),
		ClaudePath: "/Users/me/.local/bin/claude",
		LoginEnv:   LoginEnv{"PATH": "/usr/bin:/bin", "SECRET_TOKEN": "hunter2"},
		Read:       os.ReadFile,
	}, orig
}

func read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "setup", name))
	must(t, err)
	return b
}

func actions(p *Plan) map[string]string {
	out := map[string]string{}
	for _, c := range p.Changes {
		out[filepath.Base(c.Path)] = c.Action()
	}
	return out
}

func TestInitUninstallRoundTrip(t *testing.T) {
	in, orig := fakeHome(t)
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	backups := filepath.Join(in.Home, "backups")

	plan, err := PlanInit(in)
	must(t, err)
	want := map[string]string{"settings.json": "modify", "config.toml": "create", "env.json": "create"}
	if got := actions(plan); !equalMaps(got, want) {
		t.Errorf("init actions %v, want %v", got, want)
	}
	var out bytes.Buffer
	plan.Write(&out, in.Home)
	text := out.String()
	for _, s := range []string{"~/.claude/settings.json", "register acmd's hooks for 15 events; your 2 other hook entries are kept",
		`claude_command = "/Users/me/.local/bin/claude"`,
		"2 variables (values not shown)"} {
		if !strings.Contains(text, s) {
			t.Errorf("plan output lacks %q:\n%s", s, text)
		}
	}
	if strings.Contains(text, "hunter2") || strings.Contains(text, "SECRET_TOKEN") {
		t.Errorf("plan output shows the login environment:\n%s", text)
	}

	bdir, err := plan.Apply(backups, in.Home, now)
	must(t, err)
	if filepath.Base(bdir) != "20261031T120000Z" {
		t.Errorf("backup dir %s", bdir)
	}
	for _, rel := range []string{".claude/settings.json"} {
		if b, err := os.ReadFile(filepath.Join(bdir, rel)); err != nil || !bytes.Equal(b, orig[strings.Replace(rel, ".config/kitty", "dotfiles", 1)]) {
			t.Errorf("backup of %s: %v", rel, err)
		}
	}
	if fi, err := os.Stat(filepath.Join(in.Home, ".claude/settings.json")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("settings.json permissions not kept: %v", fi.Mode())
	}
	if fi, err := os.Stat(in.EnvFile); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("env.json must be 0600: %v", err)
	}
	settings, _ := os.ReadFile(in.ClaudeSettingsFile())
	if !HasHooks(settings, in.AcmdPath) {
		t.Error("hooks not registered")
	}
	cfg, err := config.Load(in.ConfigFile)
	must(t, err)
	if cfg.ClaudeCommand != in.ClaudePath {
		t.Errorf("claude_command = %q", cfg.ClaudeCommand)
	}

	// Again: nothing to do.
	again, err := PlanInit(in)
	must(t, err)
	if p := again.Pending(); len(p) != 0 {
		t.Errorf("second init not idempotent: %v", actions(again))
	}
	if d, err := again.Apply(backups, in.Home, now); err != nil || d != "" {
		t.Errorf("empty apply: %q %v", d, err)
	}

	// Uninstall restores the user's files byte for byte.
	un, err := PlanUninstall(in)
	must(t, err)
	want = map[string]string{"settings.json": "modify"}
	if got := actions(un); !equalMaps(got, want) {
		t.Errorf("uninstall actions %v, want %v", got, want)
	}
	out.Reset()
	un.Write(&out, in.Home)
	if !strings.Contains(out.String(), "remove acmd's 15 hook entries; your 2 other hook entries are kept") {
		t.Errorf("uninstall output:\n%s", out.String())
	}
	bdir2, err := un.Apply(backups, in.Home, now) // same second: must not reuse the init backup
	must(t, err)
	if bdir2 == bdir {
		t.Fatal("backup directory reused")
	}
	for rel, data := range orig {
		if b, _ := os.ReadFile(filepath.Join(in.Home, rel)); !bytes.Equal(b, data) {
			t.Errorf("%s not restored exactly:\n%s", rel, b)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(bdir, ".claude/settings.json")); !bytes.Equal(b, orig[".claude/settings.json"]) {
		t.Error("the first backup was overwritten")
	}

	// Uninstall again: nothing to do.
	un2, err := PlanUninstall(in)
	must(t, err)
	if p := un2.Pending(); len(p) != 0 {
		t.Errorf("second uninstall: %v", actions(un2))
	}
}

func TestInitFreshHome(t *testing.T) {
	home := t.TempDir()
	in := Input{
		Home: home, ConfigFile: filepath.Join(home, "cfg/config.toml"), EnvFile: filepath.Join(home, "state/env.json"),
		AcmdPath: "/opt/homebrew/bin/acmd", ClaudeDir: filepath.Join(home, ".claude"),
		Read:   os.ReadFile,
		Editor: &config.Editor{Preset: "zed", Resolved: "/usr/local/bin/zed", RunIn: "background"},
	}
	plan, err := PlanInit(in)
	must(t, err)
	want := map[string]string{"settings.json": "create", "config.toml": "create"}
	if got := actions(plan); !equalMaps(got, want) {
		t.Errorf("actions %v, want %v", got, want)
	}
	notes := strings.Join(plan.Notes, "\n")
	for _, s := range []string{"claude wasn't found"} {
		if !strings.Contains(notes, s) {
			t.Errorf("notes lack %q: %s", s, notes)
		}
	}
	bdir, err := plan.Apply(filepath.Join(home, "backups"), home, time.Now())
	must(t, err)
	if bdir != "" {
		t.Errorf("nothing to back up, got %s", bdir)
	}
	cfg, err := config.Load(in.ConfigFile)
	must(t, err)
	if cfg.Editor.Preset != "zed" || cfg.Editor.Resolved != "/usr/local/bin/zed" || cfg.ClaudeCommand != "claude" {
		t.Errorf("config: %+v", cfg)
	}

	// With no settings file, uninstall has nothing to do.
	empty := Input{Home: home, ClaudeDir: filepath.Join(home, "none"), Read: os.ReadFile}
	un, err := PlanUninstall(empty)
	must(t, err)
	if len(un.Changes) != 0 {
		t.Errorf("uninstall: %v", actions(un))
	}
}

func TestApplyRefusesChangedFiles(t *testing.T) {
	in, _ := fakeHome(t)
	plan, err := PlanInit(in)
	must(t, err)
	must(t, os.WriteFile(in.ClaudeSettingsFile(), []byte(`{"edited": true}`), 0o600))
	if _, err := plan.Apply(filepath.Join(in.Home, "b"), in.Home, time.Now()); err == nil || !strings.Contains(err.Error(), "changed since the plan") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(in.ConfigFile); !os.IsNotExist(err) {
		t.Error("files were written although the plan was stale")
	}
}

func TestPlanErrors(t *testing.T) {
	in, _ := fakeHome(t)
	must(t, os.WriteFile(in.ClaudeSettingsFile(), []byte(`[1]`), 0o600))
	if _, err := PlanInit(in); err == nil {
		t.Error("settings.json that isn't an object accepted")
	}
	if _, err := PlanUninstall(in); err == nil {
		t.Error("uninstall accepted a broken settings.json")
	}
	in2, _ := fakeHome(t)
	in2.Read = func(p string) ([]byte, error) {
		if strings.HasSuffix(p, "config.toml") {
			return nil, errors.New("denied")
		}
		return os.ReadFile(p)
	}
	if _, err := PlanInit(in2); err == nil {
		t.Error("read error should surface")
	}
}

func TestStableBinary(t *testing.T) {
	dir := t.TempDir()
	cellar := filepath.Join(dir, "Cellar", "agent-command", "0.6.0", "bin", "acmd")
	link := filepath.Join(dir, "bin", "acmd")
	for _, p := range []string{cellar, link} {
		must(t, os.MkdirAll(filepath.Dir(p), 0o755))
		must(t, os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755))
	}
	lookPath := func(name string) (string, error) {
		if name == "acmd" {
			return link, nil
		}
		return "", errors.New("not found")
	}
	cases := []struct{ argv0, exe, want string }{
		{"acmd", cellar, link},          // invoked by name: PATH entry (the Homebrew symlink)
		{"agent-command", cellar, link}, // not on PATH: executable, mapped from Cellar to bin
		{cellar, cellar, link},          // Cellar path: mapped
		{"/usr/local/bin/acmd", "", "/usr/local/bin/acmd"},
		{"", "/x/acmd", "/x/acmd"},
	}
	for _, c := range cases {
		if got := StableBinary(c.argv0, c.exe, lookPath); got != c.want {
			t.Errorf("StableBinary(%q, %q) = %q, want %q", c.argv0, c.exe, got, c.want)
		}
	}
	if got := StableBinary("./acmd", "", lookPath); !filepath.IsAbs(got) {
		t.Errorf("relative argv0 not made absolute: %s", got)
	}
}

func TestConfigDirs(t *testing.T) {
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }
	if got := ClaudeConfigDir("/h", getenv); got != "/h/.claude" {
		t.Error(got)
	}
	env["CLAUDE_CONFIG_DIR"] = "/c"
	if ClaudeConfigDir("/h", getenv) != "/c" {
		t.Error("override ignored")
	}
}

func TestRecord(t *testing.T) {
	p := filepath.Join(t.TempDir(), "setup.json")
	if _, ok, err := ReadRecord(p); ok || err != nil {
		t.Fatalf("missing record: %v %v", ok, err)
	}
	r := Record{Version: "0.7.0", At: time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC), AcmdPath: "/a", ClaudeDir: "/c", ConfigFile: "/f"}
	must(t, WriteRecord(p, r))
	got, ok, err := ReadRecord(p)
	if err != nil || !ok || got != r {
		t.Errorf("round trip: %+v %v %v", got, ok, err)
	}
	must(t, os.WriteFile(p, []byte("{"), 0o600))
	if _, _, err := ReadRecord(p); err == nil {
		t.Error("corrupt record accepted")
	}
}

func TestEditorHelpers(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"nvim", "nvim|{dir}"},
		{"nvim {dir}", "nvim|{dir}"},
		{`open -a "Sublime Text"`, "open|-a|Sublime Text|{dir}"},
		{`hx --working-dir={dir} '{dir}'`, "hx|--working-dir={dir}|{dir}"},
		{`my\ editor  -w`, "my editor|-w|{dir}"},
	}
	for _, c := range cases {
		got, err := ParseCommand(c.in)
		if err != nil || strings.Join(got, "|") != c.want {
			t.Errorf("ParseCommand(%q) = %q, %v; want %s", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "   ", `"open`, `x\`} {
		if _, err := ParseCommand(bad); err == nil {
			t.Errorf("ParseCommand(%q) accepted", bad)
		}
	}

	dir := t.TempDir()
	exe := filepath.Join(dir, "code")
	must(t, os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "zed"), []byte("x"), 0o600))
	if got, err := LookPathIn("code", "relative:"+dir); err != nil || got != exe {
		t.Errorf("LookPathIn: %q %v", got, err)
	}
	if _, err := LookPathIn("zed", dir); err == nil {
		t.Error("non-executable file found")
	}
	if _, err := LookPathIn(exe, ""); err != nil {
		t.Errorf("absolute path: %v", err)
	}
	if _, err := LookPathIn("code", "relative"); err == nil {
		t.Error("relative PATH entries must be ignored")
	}

	found := DetectEditors(func(n string) (string, error) { return LookPathIn(n, dir) },
		func(id string) bool { return id == "com.jetbrains.intellij.ce" })
	byName := map[string]DetectedEditor{}
	for _, d := range found {
		byName[d.Name] = d
	}
	if !byName["vscode"].Found() || byName["vscode"].Launcher != exe || byName["zed"].Found() || !byName["idea"].App || byName["cursor"].Found() {
		t.Errorf("detected: %+v", found)
	}
	if _, ok := PresetByName("idea"); !ok {
		t.Error("idea preset missing")
	}
	if _, ok := PresetByName("emacs"); ok {
		t.Error("unknown preset found")
	}
}

func equalMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestStableBinaryFollowsLinkIntoApp(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	must(t, err)
	binary := filepath.Join(dir, "Agent Command.app/Contents/MacOS/agent-command")
	must(t, os.MkdirAll(filepath.Dir(binary), 0o755))
	must(t, os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755))
	link := filepath.Join(dir, "acmd")
	must(t, os.Symlink(binary, link))
	if got := StableBinary(link, link, nil); got != binary {
		t.Errorf("through the link: %s", got)
	}
	if got := StableBinary(binary, binary, nil); got != binary {
		t.Errorf("direct: %s", got)
	}
	other := filepath.Join(dir, "elsewhere")
	must(t, os.WriteFile(other, []byte("#!/bin/sh\n"), 0o755))
	otherLink := filepath.Join(dir, "acmd2")
	must(t, os.Symlink(other, otherLink))
	if got := StableBinary(otherLink, otherLink, nil); got != otherLink {
		t.Errorf("a link outside an app should stay: %s", got)
	}
}

// An existing config.toml loses acmd v1's settings, and keeps the rest.
func TestInitRetiresV1Settings(t *testing.T) {
	in, _ := fakeHome(t)
	must(t, os.MkdirAll(filepath.Dir(in.ConfigFile), 0o755))
	must(t, os.WriteFile(in.ConfigFile, []byte("# mine\nsidebar_width = 30\n\n[keys]\ntoggle = \"cmd+b\"\n\n[speech]\nspeed = 1.5\n"), 0o600))
	plan, err := PlanInit(in)
	must(t, err)
	for _, c := range plan.Changes {
		if filepath.Base(c.Path) != "config.toml" {
			continue
		}
		got := string(c.New)
		if strings.Contains(got, "[keys]") || strings.Contains(got, "sidebar_width") ||
			!strings.Contains(got, "# mine") || !strings.Contains(got, "speed = 1.5") {
			t.Errorf("config.toml:\n%s", got)
		}
		return
	}
	t.Error("config.toml isn't changed")
}
