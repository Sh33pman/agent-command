package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

const acmdPath = "/opt/homebrew/bin/acmd"

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "setup", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	p := filepath.Join("..", "..", "testdata", "setup", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("%v (run with -update to create)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from golden:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func keyOrder(t *testing.T, b []byte) []string {
	t.Helper()
	o, err := parseObject(b)
	if err != nil {
		t.Fatal(err)
	}
	return o.keys
}

func TestAddHooksToUserSettings(t *testing.T) {
	in := fixture(t, "claude-settings-user.json")
	out, changed, err := AddHooks(in, acmdPath)
	if err != nil || !changed {
		t.Fatalf("AddHooks: changed=%v err=%v", changed, err)
	}
	golden(t, "claude-settings-user.added.json", out)
	if got := strings.Join(keyOrder(t, out), ","); got != "permissions,statusLine,enabledPlugins,effortLevel,hooks" {
		t.Errorf("key order not preserved: %s", got)
	}
	if !HasHooks(out, acmdPath) || HasHooks(out, "/other/acmd") || HasHooks(in, acmdPath) {
		t.Error("HasHooks wrong")
	}

	// Idempotent.
	again, changed, err := AddHooks(out, acmdPath)
	if err != nil || changed || !bytes.Equal(again, out) {
		t.Errorf("second AddHooks changed the file: changed=%v err=%v", changed, err)
	}

	// Remove restores the original content (and here even the bytes,
	// since the input uses Claude's own formatting).
	back, changed, err := RemoveHooks(out)
	if err != nil || !changed || !bytes.Equal(back, in) {
		t.Errorf("RemoveHooks(AddHooks(x)) != x: changed=%v err=%v\n%s", changed, err, back)
	}
}

func TestHooksPreserveUserHooks(t *testing.T) {
	in := fixture(t, "claude-settings-with-user-hooks.json")
	out, _, err := AddHooks(in, acmdPath)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "claude-settings-with-user-hooks.added.json", out)
	var parsed struct {
		Hooks map[string][]hookGroup `json:"hooks"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatal(err)
	}
	stop := parsed.Hooks["Stop"]
	if len(stop) != 2 || stop[0].Hooks[0].Command != "say done" || !strings.HasSuffix(stop[1].Hooks[0].Command, HookMarker) {
		t.Errorf("Stop hooks: %+v", stop)
	}
	if len(parsed.Hooks) != len(HookEvents) {
		t.Errorf("events = %d, want %d", len(parsed.Hooks), len(HookEvents))
	}

	// Moving the binary updates our entries in place (no duplicates).
	moved, changed, err := AddHooks(out, "/usr/local/bin/acmd")
	if err != nil || !changed || !HasHooks(moved, "/usr/local/bin/acmd") || strings.Contains(string(moved), acmdPath) {
		t.Errorf("path update failed: changed=%v err=%v", changed, err)
	}
	if strings.Count(string(moved), HookMarker) != len(HookEvents) {
		t.Errorf("duplicate acmd entries after path update")
	}

	back, _, err := RemoveHooks(moved)
	if err != nil {
		t.Fatal(err)
	}
	if !sameJSON(back, in) {
		t.Errorf("user hooks not restored exactly:\n%s", back)
	}
}

func TestHooksEdgeCases(t *testing.T) {
	// Missing/empty settings.json.
	out, changed, err := AddHooks(nil, acmdPath)
	if err != nil || !changed || !HasHooks(out, acmdPath) {
		t.Errorf("empty settings: %v %v", changed, err)
	}
	if back, _, _ := RemoveHooks(out); string(back) != "{}\n" {
		t.Errorf("remove from fresh: %q", back)
	}
	// No hooks at all: nothing to remove, input returned as is.
	if back, changed, err := RemoveHooks([]byte(`{"a":1}`)); err != nil || changed || string(back) != `{"a":1}` {
		t.Errorf("remove without hooks: %q %v %v", back, changed, err)
	}
	for name, bad := range map[string]string{
		"invalid":       `{`,
		"array":         `[]`,
		"trailing data": `{} {}`,
		"bad hooks":     `{"hooks": 3}`,
		"bad event":     `{"hooks": {"Stop": 3}}`,
	} {
		if _, _, err := AddHooks([]byte(bad), acmdPath); err == nil {
			t.Errorf("%s: AddHooks accepted %s", name, bad)
		}
	}
	if _, _, err := RemoveHooks([]byte(`{`)); err == nil {
		t.Error("RemoveHooks accepted invalid JSON")
	}
	// A non-list event value is left alone by RemoveHooks.
	if back, _, err := RemoveHooks([]byte(`{"hooks":{"Custom":"x"}}`)); err != nil || !strings.Contains(string(back), `"Custom": "x"`) {
		t.Errorf("non-list event: %s %v", back, err)
	}
	if HasHooks([]byte(`{`), acmdPath) || HasHooks([]byte(`{"hooks":1}`), acmdPath) {
		t.Error("HasHooks on bad input")
	}
}

func TestHookCommandQuoting(t *testing.T) {
	if got := HookCommand("/opt/homebrew/bin/acmd", "Stop"); got != "[ -x /opt/homebrew/bin/acmd ] && /opt/homebrew/bin/acmd hook Stop || true # managed by acmd" {
		t.Errorf("plain path: %s", got)
	}
	got := HookCommand("/Users/o'neil/my apps/acmd", "Stop")
	if !strings.Contains(got, `'/Users/o'\''neil/my apps/acmd'`) {
		t.Errorf("quoted path: %s", got)
	}
	// The command must work in a real shell: the guard makes it a no-op for a missing binary.
	if out, err := exec.Command("/bin/sh", "-c", HookCommand("/nonexistent/acmd", "Stop")).CombinedOutput(); err != nil || len(out) != 0 {
		t.Errorf("guarded command with missing binary: %q %v", out, err)
	}
}

func FuzzHooksRoundTrip(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"a":[1,2],"hooks":{"Stop":[{"hooks":[{"type":"command","command":"x"}]}]}}`))
	f.Add([]byte(`{"hooks":{}}`))
	f.Fuzz(func(t *testing.T, in []byte) {
		out, _, err := AddHooks(in, acmdPath)
		if err != nil {
			return
		}
		if !HasHooks(out, acmdPath) {
			t.Fatalf("hooks missing after AddHooks(%q)", in)
		}
		again, changed, err := AddHooks(out, acmdPath)
		if err != nil || changed || !bytes.Equal(again, out) {
			t.Fatalf("not idempotent for %q", in)
		}
		back, _, err := RemoveHooks(out)
		if err != nil || HasHooks(back, acmdPath) || strings.Contains(string(back), HookMarker) {
			t.Fatalf("RemoveHooks left acmd entries for %q: %s", in, back)
		}
	})
}

func TestManagedBlock(t *testing.T) {
	for _, orig := range []string{"", "font_size 12\n", "a\n\nb\n"} {
		with, changed := EnsureBlock(orig, "include ./agent-command.conf")
		if !changed || !strings.Contains(with, BlockBegin+"\ninclude ./agent-command.conf\n"+BlockEnd+"\n") {
			t.Errorf("EnsureBlock(%q) = %q", orig, with)
		}
		if again, changed := EnsureBlock(with, "include ./agent-command.conf"); changed || again != with {
			t.Errorf("not idempotent for %q", orig)
		}
		if back, removed := RemoveBlock(with); !removed || back != orig {
			t.Errorf("RemoveBlock(EnsureBlock(%q)) = %q", orig, back)
		}
	}
	// Body changes replace the block in place.
	with, _ := EnsureBlock("x\n", "one")
	replaced, changed := EnsureBlock(with, "two")
	if !changed || strings.Contains(replaced, "one") || strings.Count(replaced, BlockBegin) != 1 {
		t.Errorf("replace: %q", replaced)
	}
	// No trailing newline: still a clean block (byte-exact restore isn't promised here).
	with, _ = EnsureBlock("x", "b")
	if !strings.HasPrefix(with, "x\n\n"+BlockBegin) {
		t.Errorf("no trailing newline: %q", with)
	}
	if back, _ := RemoveBlock(with); back != "x\n" {
		t.Errorf("remove: %q", back)
	}
	// Unterminated block is left alone.
	broken := "a\n" + BlockBegin + "\nb\n"
	if back, removed := RemoveBlock(broken); removed || back != broken {
		t.Error("unterminated block must be left for the user")
	}
	if _, removed := RemoveBlock("nothing here\n"); removed {
		t.Error("removed a block that isn't there")
	}
}

func TestNormalizeKey(t *testing.T) {
	cases := []struct{ in, mod, want string }{
		{"cmd+shift+N", "", "shift+super+n"},
		{"shift+cmd+n", "", "shift+super+n"},
		{"super+shift+n", "", "shift+super+n"},
		{"kitty_mod+n", "", "ctrl+shift+n"},
		{"kitty_mod+n", "cmd+alt", "alt+super+n"},
		{"ctrl+cmd+1", "", "ctrl+super+1"},
		{"cmd+shift+/", "", "shift+super+slash"},
		{"cmd+shift+slash", "", "shift+super+slash"},
		{"option+left", "", "alt+left"},
		{"ctrl++", "", "ctrl+plus"},
		{"  F1 ", "", "f1"},
	}
	for _, c := range cases {
		if got := NormalizeKey(c.in, c.mod); got != c.want {
			t.Errorf("NormalizeKey(%q, %q) = %q, want %q", c.in, c.mod, got, c.want)
		}
	}
}

func TestParseKittyConfigAndConflicts(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"kitty.conf", "keybinds.conf", "theme.conf"} {
		if err := os.WriteFile(filepath.Join(dir, f), fixture(t, f), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// acmd's own file must be ignored even if included.
	if err := os.WriteFile(filepath.Join(dir, KittyConfName), []byte("map cmd+shift+n launch x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(dir, "kitty.conf")
	f, _ := os.OpenFile(conf, os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString("include ./" + KittyConfName + "\ninclude ./missing.conf\nallow_remote_control yes\n")
	_ = f.Close()

	kc, err := ParseKittyConfig(conf, os.ReadFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(kc.Files) != 3 {
		t.Errorf("files read: %v", kc.Files)
	}
	if kc.Settings["allow_remote_control"] != "yes" {
		t.Errorf("settings: %v", kc.Settings)
	}
	for _, k := range []string{"shift+super+right", "shift+left", "ctrl+shift+n", "shift+super+e"} {
		if _, ok := kc.Maps[k]; !ok {
			t.Errorf("map %s not found in %v", k, kc.Maps)
		}
	}

	binds := KittyBinds(acmdPath, DefaultKeys)
	defaults := ParseKittyDefaults([]byte("super+shift+/\topen_url https://sw.kovidgoyal.net/kitty/\nsuper+shift+k\tsomething\n\nbad line\n"))
	skip := Conflicts(binds, kc, defaults)
	if !strings.Contains(skip["shift+super+e"], "my-editor") {
		t.Errorf("user conflict on cmd+shift+e: %v", skip)
	}
	if !strings.Contains(skip["shift+super+k"], "Kitty's default something") {
		t.Errorf("default conflict on cmd+shift+k: %v", skip)
	}
	if len(skip) != 2 {
		t.Errorf("unexpected conflicts: %v", skip)
	}

	out := RenderKittyConf(binds, skip, KittyConfig{})
	golden(t, "agent-command.conf", []byte(out))
	if kept := RenderKittyConf(binds, skip, kc); strings.Contains(kept, "allow_remote_control") || !strings.Contains(kept, "listen_on unix:kitty-acmd-{kitty_pid}") {
		t.Errorf("the user's allow_remote_control yes should be kept:\n%s", kept)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "map ") && strings.Contains(line, "#") {
			t.Errorf("inline comment in Kitty map line (Kitty doesn't support them): %s", line)
		}
	}

	if _, err := ParseKittyConfig(filepath.Join(dir, "nope.conf"), os.ReadFile); err == nil {
		t.Error("missing kitty.conf should be an error")
	}
	if _, err := ParseKittyConfig(conf, func(string) ([]byte, error) { return nil, errors.New("denied") }); err == nil {
		t.Error("read error should surface")
	}
}

func TestKittyRemoteSettings(t *testing.T) {
	cases := []struct {
		settings  map[string]string
		lines     string
		noteParts []string
	}{
		{nil, "allow_remote_control socket-only|listen_on unix:kitty-acmd-{kitty_pid}", nil},
		{map[string]string{"allow_remote_control": "socket-only"}, "listen_on unix:kitty-acmd-{kitty_pid}", []string{"keeping your allow_remote_control socket-only"}},
		{map[string]string{"allow_remote_control": "No"}, "allow_remote_control socket-only|listen_on unix:kitty-acmd-{kitty_pid}", []string{"No is overridden"}},
		{map[string]string{"allow_remote_control": "password"}, "listen_on unix:kitty-acmd-{kitty_pid}", []string{"password-free"}},
		{map[string]string{"listen_on": "unix:/tmp/mykitty"}, "allow_remote_control socket-only", []string{"keeping your listen_on unix:/tmp/mykitty"}},
		{map[string]string{"listen_on": "none"}, "allow_remote_control socket-only|listen_on unix:kitty-acmd-{kitty_pid}", nil},
	}
	for _, c := range cases {
		lines, notes := KittyRemoteSettings(KittyConfig{Settings: c.settings})
		if got := strings.Join(lines, "|"); got != c.lines {
			t.Errorf("%v: lines %q, want %q", c.settings, got, c.lines)
		}
		all := strings.Join(notes, "\n")
		if len(c.noteParts) == 0 && all != "" {
			t.Errorf("%v: unexpected notes %q", c.settings, all)
		}
		for _, part := range c.noteParts {
			if !strings.Contains(all, part) {
				t.Errorf("%v: notes %q lack %q", c.settings, all, part)
			}
		}
	}
}

func TestKittyDefaultsScriptAgainstRealKitty(t *testing.T) {
	kitty, err := exec.LookPath("kitty")
	if err != nil {
		t.Skip("kitty not installed")
	}
	out, err := exec.Command(kitty, "+runpy", KittyDefaultsScript).Output()
	if err != nil {
		t.Skipf("kitty +runpy failed: %v", err)
	}
	d := ParseKittyDefaults(out)
	// M0: Kitty's cmd+shift+/ opens its docs, which is why the cheat sheet isn't there.
	if !strings.Contains(d["shift+super+slash"], "open_url") {
		t.Errorf("expected Kitty's default cmd+shift+/ in %d defaults", len(d))
	}
	if _, clash := d["shift+super+k"]; clash {
		t.Error("cmd+shift+k (acmd's cheat sheet) unexpectedly has a Kitty default")
	}
}

func TestLoginEnv(t *testing.T) {
	raw := []byte("PATH=/opt/homebrew/bin:/usr/bin\x00HOME=/Users/x\x00CLAUDECODE=1\x00CLAUDE_CODE_SESSION_ID=s\x00PWD=/tmp\x00MULTI=a=b\x00\x00junk\x00EMPTY=\x00")
	env := ParseEnv0(raw)
	want := LoginEnv{"PATH": "/opt/homebrew/bin:/usr/bin", "HOME": "/Users/x", "MULTI": "a=b", "EMPTY": ""}
	if len(env) != len(want) {
		t.Fatalf("env = %v", env)
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("%s = %q, want %q", k, env[k], v)
		}
	}
	if got := strings.Join(env.Environ(), ";"); got != "EMPTY=;HOME=/Users/x;MULTI=a=b;PATH=/opt/homebrew/bin:/usr/bin" {
		t.Errorf("Environ = %s", got)
	}
	b, err := env.MarshalFile()
	if err != nil || !bytes.HasSuffix(b, []byte("}\n")) {
		t.Errorf("MarshalFile: %s %v", b, err)
	}

	// Real login shell (no CLAUDE* even though this test may run inside Claude).
	got, err := CaptureLoginEnv(context.Background(), "/bin/sh")
	if err != nil {
		t.Skipf("login shell unavailable: %v", err)
	}
	for k := range got {
		if strings.HasPrefix(k, "CLAUDE") {
			t.Errorf("captured %s", k)
		}
	}
	if _, err := CaptureLoginEnv(context.Background(), "/nonexistent/shell"); err == nil {
		t.Error("missing shell should fail")
	}
}
