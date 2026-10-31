package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/setup"
)

type sandbox struct {
	home, bin string
	paths     paths.Paths
	settings  string // Claude settings.json
	kittyDir  string
	userJSON  []byte
}

// newSandbox points every location acmd setup reads or writes into a temp
// home and stubs the login shell, Kitty and Spotlight.
func newSandbox(t *testing.T) *sandbox {
	t.Helper()
	home := t.TempDir()
	s := &sandbox{home: home, bin: filepath.Join(home, "bin"), kittyDir: filepath.Join(home, ".config", "kitty")}
	s.paths = paths.Paths{StateDir: filepath.Join(home, ".local/state/agent-command"), RuntimeDir: filepath.Join(home, "run"),
		ConfigDir: filepath.Join(home, ".config/agent-command")}
	for k, v := range map[string]string{
		"HOME": home, "ACMD_STATE_DIR": s.paths.StateDir, "ACMD_RUNTIME_DIR": s.paths.RuntimeDir, "ACMD_CONFIG_DIR": s.paths.ConfigDir,
		"CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude"), "KITTY_CONFIG_DIRECTORY": s.kittyDir, "XDG_CONFIG_HOME": "",
		"ACMD_NO_AUTO_INIT": "",
	} {
		t.Setenv(k, v)
	}
	if err := os.MkdirAll(s.bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"claude", "code", "nvim", "kitty"} {
		if err := os.WriteFile(filepath.Join(s.bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	s.settings = filepath.Join(home, ".claude", "settings.json")
	s.userJSON = []byte("{\n  \"model\": \"opus\"\n}\n")
	if err := os.MkdirAll(filepath.Dir(s.settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.settings, s.userJSON, 0o600); err != nil {
		t.Fatal(err)
	}

	origCapture, origDefaults, origBundle, origApp := captureLoginEnv, kittyDefaults, hasBundle, kittyAppBinary
	captureLoginEnv = func(context.Context, string) (setup.LoginEnv, error) {
		return setup.LoginEnv{"PATH": s.bin + ":/usr/bin:/bin", "TOKEN": "secret-value"}, nil
	}
	kittyDefaults = func(context.Context, string) (map[string]string, error) {
		return map[string]string{"shift+super+a": "select_all"}, nil
	}
	hasBundle = func(id string) bool { return id == "dev.zed.Zed" }
	kittyAppBinary = filepath.Join(home, "no-kitty-app")
	t.Cleanup(func() {
		captureLoginEnv, kittyDefaults, hasBundle, kittyAppBinary = origCapture, origDefaults, origBundle, origApp
	})
	return s
}

// testArgv0 is the path acmd runs as in tests.
var testArgv0 = "/opt/homebrew/bin/acmd"

func execute(t *testing.T, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	root := NewRootCmd("acmd", &out, &errOut)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	invokedAs = testArgv0
	err = root.Execute()
	return out.String(), errOut.String(), err
}

func TestInitDryRunChangesNothing(t *testing.T) {
	s := newSandbox(t)
	out, _, err := execute(t, "", "init", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"dry run: nothing is changed", "~/.claude/settings.json", "register acmd's hooks for 15 events",
		"[ -x /opt/homebrew/bin/acmd ] && /opt/homebrew/bin/acmd hook <Event> || true # managed by acmd",
		"create    ~/.config/kitty/kitty.conf", "map cmd+shift+b launch --type=background --allow-remote-control /opt/homebrew/bin/acmd toggle",
		"skipped cmd+shift+a (next-attention): conflicts with Kitty's default select_all", "claude_command = \"" + s.bin + "/claude\"", "2 variables (values not shown)", "Editor: not chosen yet"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "secret-value") {
		t.Error("dry run printed an environment value")
	}
	if b, _ := os.ReadFile(s.settings); !bytes.Equal(b, s.userJSON) {
		t.Error("dry run changed settings.json")
	}
	for _, p := range []string{s.paths.StateDir, s.paths.ConfigDir, s.kittyDir} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("dry run created %s", p)
		}
	}
}

func TestInitEditorUninstall(t *testing.T) {
	s := newSandbox(t)
	out, _, err := execute(t, "", "init")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Done.") || !strings.Contains(out, "Originals backed up to ~/.local/state/agent-command/backups/") ||
		!strings.Contains(out, "Restart Kitty") {
		t.Errorf("init output:\n%s", out)
	}
	settings, _ := os.ReadFile(s.settings)
	if !setup.HasHooks(settings, "/opt/homebrew/bin/acmd") {
		t.Error("hooks not registered")
	}
	rec, ok, err := setup.ReadRecord(s.paths.Setup())
	if err != nil || !ok || rec.KittyDir != s.kittyDir || rec.AcmdPath != "/opt/homebrew/bin/acmd" {
		t.Errorf("record: %+v %v %v", rec, ok, err)
	}
	if fi, err := os.Stat(s.paths.StateDir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("state dir: %v", err)
	}
	claude, env, err := claudeCommand(s.paths)
	if err != nil || claude != filepath.Join(s.bin, "claude") || !contains(env, "TOKEN=secret-value") {
		t.Errorf("claudeCommand = %s %v", claude, err)
	}

	// Editors.
	if _, _, err := execute(t, "", "editor", "set", "cursor"); err == nil {
		t.Error("cursor isn't installed in the sandbox")
	}
	if _, _, err := execute(t, "", "editor", "set", "emacs"); err == nil {
		t.Error("unknown preset accepted")
	}
	if _, _, err := execute(t, "", "editor", "set", "zed"); err != nil {
		t.Errorf("zed (app only): %v", err)
	}
	if _, _, err := execute(t, "", "editor", "set", "vscode"); err != nil {
		t.Fatal(err)
	}
	out, _, _ = execute(t, "", "editor", "show")
	if !strings.Contains(out, "preset:   vscode") || !strings.Contains(out, "resolved: "+s.bin+"/code") {
		t.Errorf("editor show:\n%s", out)
	}
	if _, _, err := execute(t, "", "editor", "set", "custom", "--run-in", "tab", "--", "nvim", "-p"); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load(s.paths.Config())
	if cfg.Editor.Preset != "custom" || strings.Join(cfg.Editor.Command, " ") != "nvim -p {dir}" || cfg.Editor.RunIn != "tab" {
		t.Errorf("custom editor: %+v", cfg.Editor)
	}
	for _, bad := range [][]string{{"editor", "set", "custom"}, {"editor", "set", "custom", "--run-in", "x", "--", "nvim"},
		{"editor", "set", "custom", "--", "no-such-editor"}, {"editor", "set", "vscode", "extra"}} {
		if _, _, err := execute(t, "", bad...); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	out, _, _ = execute(t, "", "editor", "detect")
	if !strings.Contains(out, "vscode  VS Code        ✓ "+s.bin+"/code") || !strings.Contains(out, "app installed") || !strings.Contains(out, "not found") {
		t.Errorf("editor detect:\n%s", out)
	}

	// Again: nothing to change; the custom editor is left alone.
	out, _, err = execute(t, "", "init")
	if err != nil || !strings.Contains(out, "Already set up; nothing changed.") {
		t.Errorf("second init: %v\n%s", err, out)
	}

	// Uninstall.
	out, _, err = execute(t, "", "uninstall", "--dry-run")
	if err != nil || !strings.Contains(out, "remove acmd's 15 hook entries") || !strings.Contains(out, "stop      the acmd daemon") {
		t.Errorf("uninstall dry run: %v\n%s", err, out)
	}
	if _, _, err := execute(t, "", "uninstall"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("uninstall without a terminal or --yes: %v", err)
	}
	out, _, err = execute(t, "", "uninstall", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(s.settings); !bytes.Equal(b, s.userJSON) {
		t.Errorf("settings.json not restored:\n%s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(s.kittyDir, "kitty.conf")); len(b) != 0 {
		t.Errorf("kitty.conf: %q", b)
	}
	if _, ok, _ := setup.ReadRecord(s.paths.Setup()); ok {
		t.Error("setup record kept")
	}
	if _, err := os.Stat(s.paths.Config()); err != nil {
		t.Error("--keep-data is the default: config.toml must stay")
	}
	if !strings.Contains(out, "Restart Kitty") {
		t.Errorf("uninstall output:\n%s", out)
	}

	// Delete data: everything but the backups.
	if _, _, err := execute(t, "", "uninstall", "--yes", "--keep-data=false"); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(s.paths.StateDir)
	if len(entries) != 1 || entries[0].Name() != "backups" {
		t.Errorf("state left: %v", entries)
	}
	if _, err := os.Stat(s.paths.ConfigDir); !os.IsNotExist(err) {
		t.Error("config dir not deleted")
	}
}

func TestGhosttyNotYet(t *testing.T) {
	newSandbox(t)
	if _, _, err := execute(t, "", "init", "--ghostty"); err == nil {
		t.Error("--ghostty should say it isn't available yet")
	}
}

func TestAutoInitHintWithoutTerminal(t *testing.T) {
	s := newSandbox(t)
	// list fails (no daemon in the sandbox); what matters is the hint and that nothing changed.
	spawn := spawnDaemon
	spawnDaemon = func(paths.Paths) error { return os.ErrNotExist }
	t.Cleanup(func() { spawnDaemon = spawn })
	_, errOut, _ := execute(t, "", "list")
	if !strings.Contains(errOut, "acmd isn't set up yet: run `acmd init`") {
		t.Errorf("stderr: %q", errOut)
	}
	if b, _ := os.ReadFile(s.settings); !bytes.Equal(b, s.userJSON) {
		t.Error("non-interactive first run changed settings")
	}
	t.Setenv("ACMD_NO_AUTO_INIT", "1")
	if _, errOut, _ = execute(t, "", "list"); strings.Contains(errOut, "set up") {
		t.Errorf("ACMD_NO_AUTO_INIT ignored: %q", errOut)
	}
	// Commands outside the list never prompt.
	t.Setenv("ACMD_NO_AUTO_INIT", "")
	if _, errOut, _ = execute(t, "", "version"); errOut != "" {
		t.Errorf("version printed %q", errOut)
	}
}

func TestFirstRunAsks(t *testing.T) {
	s := newSandbox(t)
	ctx := context.Background()
	opts := initOptions{ask: true, interactive: true}

	var out bytes.Buffer
	done, err := runInit(ctx, strings.NewReader("n\n"), &out, opts)
	if err != nil || done {
		t.Fatalf("declined: %v %v", done, err)
	}
	if !strings.Contains(out.String(), "Set up acmd now? [Y/n]") || !strings.Contains(out.String(), "Skipped; nothing was changed") ||
		!strings.Contains(out.String(), "you'll be asked which editor") {
		t.Errorf("output:\n%s", out.String())
	}
	if b, _ := os.ReadFile(s.settings); !bytes.Equal(b, s.userJSON) {
		t.Error("declined setup changed settings")
	}

	// Enter accepts; then the editor question (default: the first detected, VS Code).
	out.Reset()
	done, err = runInit(ctx, strings.NewReader("\n\n"), &out, opts)
	if err != nil || !done {
		t.Fatalf("accepted: %v %v\n%s", done, err, out.String())
	}
	if !strings.Contains(out.String(), "1) VS Code        code    ✓ detected") || !strings.Contains(out.String(), "3) Zed            zed     ✓ detected (app)") {
		t.Errorf("picker:\n%s", out.String())
	}
	cfg, _ := config.Load(s.paths.Config())
	if cfg.Editor.Preset != "vscode" || cfg.Editor.Resolved != s.bin+"/code" {
		t.Errorf("editor: %+v", cfg.Editor)
	}
}

func TestDoctor(t *testing.T) {
	s := newSandbox(t)
	acmd := filepath.Join(s.bin, "acmd")
	if err := os.WriteFile(acmd, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	origArgv0, origOutput := testArgv0, versionOutput
	testArgv0 = acmd
	versionOutput = func(_ context.Context, name string, _ ...string) ([]byte, error) {
		return map[string][]byte{"claude": []byte("2.1.320 (Claude Code)\n"), "kitty": []byte("kitty 0.49.2 created by Kovid Goyal\n")}[filepath.Base(name)], nil
	}
	t.Cleanup(func() { testArgv0, versionOutput = origArgv0, origOutput })
	t.Setenv("KITTY_WINDOW_ID", "")

	out, _, err := execute(t, "", "doctor")
	if !errors.Is(err, ErrSilent) || !strings.Contains(out, "✗ setup hasn't run yet") || !strings.Contains(out, "· not running") {
		t.Errorf("before init: %v\n%s", err, out)
	}
	if _, err := os.Stat(s.paths.StateDir); !os.IsNotExist(err) {
		t.Error("doctor created the state dir")
	}

	if _, _, err := execute(t, "", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "", "editor", "set", "vscode"); err != nil {
		t.Fatal(err)
	}
	out, _, err = execute(t, "", "doctor")
	if err != nil || !strings.Contains(out, "Everything looks good.") || !strings.Contains(out, "✓ setup is up to date") ||
		!strings.Contains(out, "✓ hooks registered for all 15 events") || !strings.Contains(out, "✓ kitty 0.49.2") {
		t.Errorf("after init: %v\n%s", err, out)
	}

	// Moving the binary is caught (hooks, shortcuts, and init would rewrite both).
	if err := os.Remove(acmd); err != nil {
		t.Fatal(err)
	}
	out, _, err = execute(t, "", "doctor")
	if !errors.Is(err, ErrSilent) || !strings.Contains(out, "hooks run "+setup.Tilde(acmd, s.home)+", which doesn't exist") ||
		!strings.Contains(out, "shortcuts run") {
		t.Errorf("binary gone: %v\n%s", err, out)
	}
}

func TestPickEditor(t *testing.T) {
	bin := newSandbox(t).bin
	pick := func(input string) (*config.Editor, string) {
		var out bytes.Buffer
		e := pickEditor(bufio.NewReader(strings.NewReader(input)), &out, bin)
		return e, out.String()
	}
	if e, _ := pick("6\n"); e != nil {
		t.Errorf("skip: %+v", e)
	}
	if e, _ := pick(""); e != nil {
		t.Errorf("EOF: %+v", e)
	}
	if e, out := pick("9\nx\n3\n"); e == nil || e.Preset != "zed" || e.Resolved != "" || strings.Count(out, "Please enter a number") != 2 {
		t.Errorf("retry then zed: %+v\n%s", e, out)
	}
	e, out := pick("5\n\"unterminated\nno-such-editor\nnvim -O\ny\n")
	if e == nil || e.Preset != "custom" || strings.Join(e.Command, " ") != "nvim -O {dir}" || e.RunIn != "tab" || e.Resolved != bin+"/nvim" {
		t.Errorf("custom: %+v\n%s", e, out)
	}
	if e, _ := pick("5\n"); e != nil {
		t.Errorf("custom then EOF: %+v", e)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
