package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

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

func TestPlainCommandOpensTheApp(t *testing.T) {
	var got []bool
	orig := openApp
	openApp = func(_ io.Writer, newInstance bool) error {
		got = append(got, newInstance)
		return nil
	}
	t.Cleanup(func() { openApp = orig })
	if _, err := run(t, "acmd"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "acmd", "--new-instance"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] || !got[1] {
		t.Errorf("opened: %v", got)
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
