package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

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
	want := version.Get().String() + "\n"
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
	if got != version.Get() {
		t.Errorf("version --json = %+v, want %+v", got, version.Get())
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
