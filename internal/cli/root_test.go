package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/term"
	"github.com/brettmostert/agent-command/internal/version"
)

func run(t *testing.T, argv0 string, args ...string) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err := Execute(argv0, args, &out, &errOut)
	return out.String(), err
}

func TestVersion(t *testing.T) {
	out, err := run(t, "/opt/homebrew/bin/acmd", "version")
	if err != nil {
		t.Fatal(err)
	}
	info := version.Get()
	info.Screen = term.ScreenKind()
	want := info.String() + "\n"
	if out != want {
		t.Errorf("version output = %q, want %q", out, want)
	}
}

func TestVersionJSON(t *testing.T) {
	out, err := run(t, "agent-command", "version", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got version.Info
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("version --json is not JSON: %v\n%s", err, out)
	}
	want := version.Get()
	want.Screen = term.ScreenKind()
	if got != want {
		t.Errorf("version --json = %+v, want %+v", got, want)
	}
}

func TestVersionRejectsArgs(t *testing.T) {
	if _, err := run(t, "acmd", "version", "extra"); err == nil {
		t.Error("expected an error for unexpected arguments")
	}
}

func TestHelpUsesInvokedName(t *testing.T) {
	for _, name := range []string{"acmd", "agent-command"} {
		out, err := run(t, "/usr/local/bin/"+name, "--help")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, name+" [command]") {
			t.Errorf("help for %s should mention %q:\n%s", name, name+" [command]", out)
		}
	}
}

func TestCompletionZsh(t *testing.T) {
	out, err := run(t, "agent-command", "completion", "zsh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "#compdef agent-command") {
		t.Errorf("zsh completion missing #compdef header:\n%.200s", out)
	}
}

func TestUnknownCommand(t *testing.T) {
	if _, err := run(t, "acmd", "no-such-command"); err == nil {
		t.Error("expected an error for an unknown command")
	}
}

// fakeUIEnv makes this machine look like e to a bare acmd.
func fakeUIEnv(t *testing.T, e uiEnv) {
	t.Helper()
	orig := localUIEnv
	localUIEnv = func(*cobra.Command) uiEnv { return e }
	t.Cleanup(func() { localUIEnv = orig })
}

func TestPlainCommandOpensTheApp(t *testing.T) {
	var got []bool
	orig := openApp
	openApp = func(_ io.Writer, newInstance bool) error {
		got = append(got, newInstance)
		return nil
	}
	t.Cleanup(func() { openApp = orig })
	fakeUIEnv(t, uiEnv{goos: "darwin", terminal: true, hasApp: true})
	if _, err := run(t, "acmd"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "acmd", "--new-instance"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] || !got[1] {
		t.Errorf("opened: %v", got)
	}
	// Not at a terminal: help, not a UI.
	fakeUIEnv(t, uiEnv{goos: "darwin", hasApp: true})
	if out, err := run(t, "acmd"); err != nil || !strings.Contains(out, "Usage:") || len(got) != 2 {
		t.Errorf("no terminal: %v %q, opened %v", err, out, got)
	}
}

func TestChooseUI(t *testing.T) {
	for _, c := range []struct {
		name string
		e    uiEnv
		want string
		err  string
	}{
		{"a Mac at its screen", uiEnv{goos: "darwin", terminal: true, hasApp: true}, uiApp, ""},
		{"a Mac over SSH", uiEnv{goos: "darwin", terminal: true, hasApp: true, ssh: true}, uiTerminal, ""},
		{"a Mac without the app", uiEnv{goos: "darwin", terminal: true}, uiTerminal, ""},
		{"Linux with a desktop and the UI", uiEnv{goos: "linux", terminal: true, hasApp: true, display: true}, uiApp, ""},
		{"Linux with the UI and no desktop", uiEnv{goos: "linux", terminal: true, hasApp: true}, uiTerminal, ""},
		{"a headless Linux box", uiEnv{goos: "linux", terminal: true}, uiTerminal, ""},
		{"Windows", uiEnv{goos: "windows", terminal: true}, uiTerminal, ""},
		{"no terminal", uiEnv{goos: "darwin", hasApp: true}, uiHelp, ""},
		{"--terminal on a Mac", uiEnv{goos: "darwin", terminal: true, hasApp: true, inTerm: true}, uiTerminal, ""},
		{"--terminal with no terminal", uiEnv{goos: "linux", inTerm: true}, "", "needs a terminal"},
		{"--app over SSH", uiEnv{goos: "darwin", terminal: true, hasApp: true, ssh: true, app: true}, uiApp, ""},
		{"--app with none", uiEnv{goos: "windows", terminal: true, app: true}, "", "no Agent Command app"},
		{"both", uiEnv{goos: "darwin", app: true, inTerm: true}, "", "pick one"},
	} {
		got, err := chooseUI(c.e)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%s: %q %v, want an error with %q", c.name, got, err, c.err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s: %q %v, want %q", c.name, got, err, c.want)
		}
	}
}

func TestOpenArgs(t *testing.T) {
	for _, c := range []struct {
		acmd string
		new  bool
		want string
	}{
		{"/opt/homebrew/bin/acmd", false, "-b|" + bundleID},
		{"/opt/homebrew/bin/acmd", true, "-n|-b|" + bundleID + "|--args|--new-instance"},
		// A dev build: the app runs it too.
		{"/src/dist/acmd", false, "-b|" + bundleID + "|--env|ACMD_BIN=/src/dist/acmd"},
		{"", false, "-b|" + bundleID},
	} {
		if got := strings.Join(openArgs(c.acmd, c.new), "|"); got != c.want {
			t.Errorf("openArgs(%q, %v) = %q, want %q", c.acmd, c.new, got, c.want)
		}
	}
}
