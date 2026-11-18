package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteSessionsCommands(t *testing.T) {
	env(t)
	dir := t.TempDir()
	t.Setenv("ACMD_CONFIG_DIR", dir)

	out := mustRun(t, "remote", "sessions")
	if want := "Hosting: off\nNo Remote sessions. Make one with acmd remote on <session>.\n"; out != want {
		t.Errorf("no Remote sessions:\n%s", out)
	}

	out = mustRun(t, "remote", "on", "1", "fix")
	for _, want := range []string{`"refactor auth middleware" is Remote.`, `"fix flaky e2e test" is Remote.`, hostingOffNote} {
		if !strings.Contains(out, want) {
			t.Errorf("remote on lacks %q:\n%s", want, out)
		}
	}
	rows := listJSON(t)
	if !rows[0].Remote || !rows[1].Remote || rows[2].Remote {
		t.Errorf("list --json: %+v", rows)
	}

	out = mustRun(t, "remote", "sessions")
	for _, want := range []string{"Hosting: off", "until Hosting is on", "CLIENTS", "refactor auth middleware", "fix flaky e2e test"} {
		if !strings.Contains(out, want) {
			t.Errorf("remote sessions lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "docs: hooks reference") {
		t.Errorf("a local session is listed:\n%s", out)
	}

	if out := mustRun(t, "remote", "off", "refactor"); out != "\"refactor auth middleware\" is local.\n" {
		t.Errorf("remote off: %q", out)
	}

	// With Hosting on: where it listens, and no note.
	must(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[remote.host]\nenabled = true\nlisten = \"all\"\n"), 0o600))
	if out := mustRun(t, "remote", "on", "docs"); strings.Contains(out, hostingOffNote) {
		t.Errorf("the Hosting note with Hosting on:\n%s", out)
	}
	out = mustRun(t, "remote", "sessions")
	if !strings.HasPrefix(out, "Hosting: on, at 0.0.0.0:7433\n\n") || strings.Contains(out, "until Hosting") {
		t.Errorf("Hosting on:\n%s", out)
	}
	var got remoteJSON
	must(t, json.Unmarshal([]byte(mustRun(t, "remote", "sessions", "--json")), &got))
	if !got.Hosting || got.Address != "0.0.0.0:7433" || len(got.Sessions) != 2 ||
		got.Sessions[0].Name != "fix flaky e2e test" || got.Sessions[1].Name != "docs: hooks reference" || got.Sessions[0].Clients != 0 {
		t.Errorf("--json: %+v", got)
	}

	if _, err := acmd(t, "remote", "on", "zzz"); err == nil || !strings.Contains(err.Error(), "no session matches") {
		t.Errorf("unknown session: %v", err)
	}
	must(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[remote\n"), 0o600))
	if _, err := acmd(t, "remote", "sessions"); err == nil {
		t.Error("a broken config is an error")
	}
}

func TestRemoteSessionsJSONWithHostingOff(t *testing.T) {
	env(t)
	t.Setenv("ACMD_CONFIG_DIR", t.TempDir())
	var got remoteJSON
	must(t, json.Unmarshal([]byte(mustRun(t, "remote", "sessions", "--json")), &got))
	if got.Hosting || got.Address != "" || got.Sessions == nil || len(got.Sessions) != 0 {
		t.Errorf("--json: %+v", got)
	}
}

func TestRemoteFlag(t *testing.T) {
	for _, tc := range []struct {
		remote, noRemote bool
		want             string // "nil", "true", "false" or "error"
	}{
		{false, false, "nil"},
		{true, false, "true"},
		{false, true, "false"},
		{true, true, "error"},
	} {
		got, err := remoteFlag(tc.remote, tc.noRemote)
		s := "nil"
		switch {
		case err != nil:
			s = "error"
		case got != nil && *got:
			s = "true"
		case got != nil:
			s = "false"
		}
		if s != tc.want {
			t.Errorf("remoteFlag(%v, %v) = %s, want %s", tc.remote, tc.noRemote, s, tc.want)
		}
	}
	if _, err := acmd(t, "new", "--remote", "--no-remote"); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Errorf("acmd new --remote --no-remote: %v", err)
	}
}
