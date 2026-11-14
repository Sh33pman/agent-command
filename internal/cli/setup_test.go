package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/setup"
)

type sandbox struct {
	home, bin string
	paths     paths.Paths
	settings  string // Claude settings.json
	userJSON  []byte
}

// newSandbox points every location acmd setup reads or writes into a temp home
// and stubs the login shell and Spotlight.
func newSandbox(t *testing.T) *sandbox {
	t.Helper()
	home := t.TempDir()
	s := &sandbox{home: home, bin: filepath.Join(home, "bin")}
	s.paths = paths.Paths{StateDir: filepath.Join(home, ".local/state/agent-command"), RuntimeDir: filepath.Join(home, "run"),
		ConfigDir: filepath.Join(home, ".config/agent-command")}
	for k, v := range map[string]string{
		"HOME": home, "ACMD_STATE_DIR": s.paths.StateDir, "ACMD_RUNTIME_DIR": s.paths.RuntimeDir, "ACMD_CONFIG_DIR": s.paths.ConfigDir,
		"CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude"), "ACMD_NO_AUTO_INIT": "",
	} {
		t.Setenv(k, v)
	}
	if err := os.MkdirAll(s.bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.bin, "claude"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	s.settings = filepath.Join(home, ".claude", "settings.json")
	s.userJSON = []byte("{\n  \"model\": \"opus\"\n}\n")
	if err := os.MkdirAll(filepath.Dir(s.settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.settings, s.userJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	origCapture, origBundle := captureLoginEnv, hasBundle
	captureLoginEnv = func(context.Context, string) (setup.LoginEnv, error) {
		return setup.LoginEnv{"PATH": s.bin + ":/usr/bin:/bin", "TOKEN": "secret-value"}, nil
	}
	hasBundle = func(id string) bool { return id == "dev.zed.Zed" }
	t.Cleanup(func() { captureLoginEnv, hasBundle = origCapture, origBundle })
	return s
}

// testArgv0 is the path acmd runs as in tests.
var testArgv0 = "/Applications/Agent Command.app/Contents/MacOS/agent-command"

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
	for _, want := range []string{"dry run: nothing is changed", "~/.claude/settings.json", "register acmd's hooks",
		"'" + testArgv0 + "' hook <Event> || true # managed by acmd"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "secret-value") {
		t.Error("printed a login-shell value")
	}
	if b, _ := os.ReadFile(s.settings); !bytes.Equal(b, s.userJSON) {
		t.Errorf("settings changed: %s", b)
	}
	if _, err := os.Stat(s.paths.Setup()); !os.IsNotExist(err) {
		t.Errorf("setup.json: %v", err)
	}
}

func TestInitUninstall(t *testing.T) {
	s := newSandbox(t)
	if _, _, err := execute(t, "", "init"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(s.settings)
	if !strings.Contains(string(b), "managed by acmd") || !strings.Contains(string(b), `"model": "opus"`) {
		t.Errorf("settings after init: %s", b)
	}
	env, err := os.ReadFile(s.paths.Env())
	if err != nil || !strings.Contains(string(env), "secret-value") {
		t.Errorf("env.json: %s, %v", env, err)
	}
	if st, _ := os.Stat(s.paths.Env()); st == nil || st.Mode().Perm() != 0o600 {
		t.Errorf("env.json mode: %v", st)
	}
	cfg, _ := os.ReadFile(s.paths.Config())
	if !strings.Contains(string(cfg), filepath.Join(s.bin, "claude")) {
		t.Errorf("config: %s", cfg)
	}
	if out, _, err := execute(t, "", "init"); err != nil || !strings.Contains(out, "Already set up") {
		t.Errorf("rerun: %v\n%s", err, out)
	}
	if _, _, err := execute(t, "", "uninstall", "--yes"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(s.settings); strings.Contains(string(b), "managed by acmd") {
		t.Errorf("hooks left: %s", b)
	}
	if _, err := os.Stat(s.paths.Setup()); !os.IsNotExist(err) {
		t.Errorf("setup.json after uninstall: %v", err)
	}
}

func TestAutoInitHintWithoutTerminal(t *testing.T) {
	newSandbox(t)
	_, errOut, _ := execute(t, "", "list")
	if !strings.Contains(errOut, "acmd init") {
		t.Errorf("no hint: %q", errOut)
	}
}

func TestDoctor(t *testing.T) {
	newSandbox(t)
	if _, _, err := execute(t, "", "init"); err != nil {
		t.Fatal(err)
	}
	out, _, _ := execute(t, "", "doctor")
	if !strings.Contains(out, "Claude Code") || !strings.Contains(out, "Daemon") {
		t.Errorf("doctor:\n%s", out)
	}
	if strings.Contains(out, "Kitty") || strings.Contains(out, "Ghostty") {
		t.Errorf("doctor mentions a terminal:\n%s", out)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestDoctorFixReplacesStaleHooks(t *testing.T) {
	s := newSandbox(t)
	// doctor checks that the hooks' binary exists, so it can't be the real app's.
	binary := filepath.Join(s.bin, "acmd")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	invoked := testArgv0
	testArgv0 = binary
	t.Cleanup(func() { testArgv0 = invoked })
	stale := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"[ -x /gone/acmd ] && /gone/acmd hook Stop || true # managed by acmd","timeout":2}]}],` +
		`"Retired":[{"hooks":[{"type":"command","command":"/gone/acmd hook Retired # managed by acmd","timeout":2}]}]}}`
	if err := os.WriteFile(s.settings, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "", "doctor", "--fix"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(s.settings)
	if strings.Contains(string(b), "/gone/acmd") || strings.Contains(string(b), "Retired") {
		t.Errorf("stale hooks left: %s", b)
	}
	if !setup.HasHooks(b, testArgv0) {
		t.Errorf("hooks not added: %s", b)
	}
}

func TestBrewCommand(t *testing.T) {
	for args, want := range map[string]string{
		"501 /opt/homebrew/Library/Homebrew/vendor/ruby --disable=gems /opt/homebrew/Library/Homebrew/brew.rb uninstall --cask x": "uninstall",
		"501 ruby /opt/homebrew/Library/Homebrew/brew.rb upgrade --cask x":                                                        "upgrade",
		"501 ruby /opt/homebrew/Library/Homebrew/brew.rb reinstall --cask x":                                                      "reinstall",
		"501 /bin/zsh": "",
		"":             "",
	} {
		if got := brewCommand(args); got != want {
			t.Errorf("brewCommand(%q) = %q, want %q", args, got, want)
		}
	}
}

// brewUninstall runs the cask's uninstall as a given Homebrew command, and
// reports which hosts it ended and what it asked.
func brewUninstall(t *testing.T, s *sandbox, args, answer string) (ended []string, asked bool) {
	t.Helper()
	if err := os.MkdirAll(s.paths.RuntimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.paths.HostSocket("01ABCDEFGHIJKLMNOPQRSTUVWX"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	origArgs, origAsk, origKill := brewArgs, askTerminal, killHost
	t.Cleanup(func() { brewArgs, askTerminal, killHost = origArgs, origAsk, origKill })
	brewArgs = func() string { return args }
	askTerminal = func(string) (string, bool) {
		asked = true
		return answer, true
	}
	killHost = func(socket string) error {
		ended = append(ended, socket)
		return nil
	}
	if _, _, err := execute(t, "", "uninstall", "--brew"); err != nil {
		t.Fatal(err)
	}
	return ended, asked
}

func TestBrewUpgradeKeepsSessionsAndHooks(t *testing.T) {
	for _, command := range []string{"upgrade", "reinstall", ""} {
		s := newSandbox(t)
		if _, _, err := execute(t, "", "init"); err != nil {
			t.Fatal(err)
		}
		args := ""
		if command != "" {
			args = "ruby brew.rb " + command + " --cask x"
		}
		ended, asked := brewUninstall(t, s, args, "n")
		if len(ended) != 0 || asked {
			t.Errorf("%q: ended %v, asked %v", command, ended, asked)
		}
		if b, _ := os.ReadFile(s.settings); !strings.Contains(string(b), "managed by acmd") {
			t.Errorf("%q removed the hooks", command)
		}
	}
}

func TestBrewUninstallEndsSessionsAndAsks(t *testing.T) {
	for _, c := range []struct {
		args, answer    string
		wantAsk, keptDB bool
	}{
		{"ruby brew.rb uninstall --cask x", "\n", true, true},
		{"ruby brew.rb uninstall --cask x", "n\n", true, false},
		{"ruby brew.rb rm --cask x", "y\n", true, true},
		{"ruby brew.rb uninstall --zap --cask x", "n\n", false, true},
	} {
		s := newSandbox(t)
		if _, _, err := execute(t, "", "init"); err != nil {
			t.Fatal(err)
		}
		ended, asked := brewUninstall(t, s, c.args, c.answer)
		if len(ended) != 1 || asked != c.wantAsk {
			t.Errorf("%q: ended %v, asked %v", c.args, ended, asked)
		}
		if b, _ := os.ReadFile(s.settings); strings.Contains(string(b), "managed by acmd") {
			t.Errorf("%q left the hooks", c.args)
		}
		if _, err := os.Stat(s.paths.Config()); (err == nil) != c.keptDB {
			t.Errorf("%q answer %q: config kept = %v", c.args, c.answer, err == nil)
		}
	}
}

func TestUninstallRemovesRuntimeFiles(t *testing.T) {
	for name, other := range map[string]bool{"only acmd's files": false, "someone else's file too": true} {
		s := newSandbox(t)
		if _, _, err := execute(t, "", "init"); err != nil {
			t.Fatal(err)
		}
		if err := s.paths.Ensure(); err != nil {
			t.Fatal(err)
		}
		dir := s.paths.RuntimeDir
		for _, f := range []string{s.paths.Lock(), s.paths.OldPopupLock(), s.paths.Socket(), s.paths.HostIDFile("01ABCDEFGHIJKLMNOP"),
			s.paths.TermIDFile("01ABCDEFGHIJKLMNOP"), filepath.Join(dir, "tts-12ab.sock")} {
			if err := os.WriteFile(f, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		keep := filepath.Join(dir, "not-acmds.txt")
		if other {
			if err := os.WriteFile(keep, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if _, _, err := execute(t, "", "uninstall", "--yes"); err != nil {
			t.Fatal(err)
		}
		left, _ := filepath.Glob(filepath.Join(dir, "*"))
		switch {
		case other && (len(left) != 1 || left[0] != keep):
			t.Errorf("%s: left %v", name, left)
		case !other:
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Errorf("%s: the folder is still there, with %v", name, left)
			}
		}
	}
}

func TestBrewUpgradeLeavesRuntimeFiles(t *testing.T) {
	s := newSandbox(t)
	if _, _, err := execute(t, "", "init"); err != nil {
		t.Fatal(err)
	}
	if err := s.paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.paths.Lock(), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	brewUninstall(t, s, "ruby brew.rb upgrade --cask x", "n")
	if _, err := os.Stat(s.paths.Lock()); err != nil {
		t.Errorf("an upgrade removed the lock: %v", err)
	}
}

func TestInitHookBinary(t *testing.T) {
	s := newSandbox(t)
	final := "/Applications/Agent Command.app/Contents/MacOS/agent-command"
	invoked := testArgv0
	testArgv0 = "/opt/homebrew/Caskroom/agent-command/1/Agent Command.app/Contents/MacOS/agent-command"
	t.Cleanup(func() { testArgv0 = invoked })
	if _, _, err := execute(t, "", "init", "--hook-binary", final); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(s.settings)
	if !setup.HasHooks(b, final) || strings.Contains(string(b), "Caskroom") {
		t.Errorf("settings: %s", b)
	}
}

func TestUseUserTempDirIgnoresHomebrewTMPDIR(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin only")
	}
	want := os.TempDir()
	t.Setenv("TMPDIR", "/private/tmp")
	useUserTempDir()
	if got := os.Getenv("TMPDIR"); got == "/private/tmp" || got == "" {
		t.Errorf("TMPDIR = %q (test started with %q)", got, want)
	}
}
