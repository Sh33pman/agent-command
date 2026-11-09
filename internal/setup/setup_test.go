package setup

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestHookPaths(t *testing.T) {
	quoted := "/Users/o'neil/my apps/acmd"
	in, _, err := AddHooks([]byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"/usr/bin/true"}]}]}}`), quoted)
	if err != nil {
		t.Fatal(err)
	}
	if paths, events := HookPaths(in); strings.Join(paths, "|") != quoted || events != len(HookEvents) {
		t.Errorf("HookPaths = %q, %d", paths, events)
	}
	// One event moved to another binary by hand.
	enc := func(s string) []byte { b, _ := json.Marshal(s); return b }
	mixed := bytes.Replace(in, enc(HookCommand(quoted, "Stop")), enc(HookCommand("/old/acmd", "Stop")), 1)
	if paths, events := HookPaths(mixed); strings.Join(paths, "|") != "/Users/o'neil/my apps/acmd|/old/acmd" || events != len(HookEvents) {
		t.Errorf("mixed: %q, %d", paths, events)
	}
	for _, bad := range []string{`{`, `{"hooks":1}`, `{"model":"x"}`, `{"hooks":{"Stop":5}}`} {
		if paths, events := HookPaths([]byte(bad)); paths != nil || events != 0 {
			t.Errorf("%s: %q, %d", bad, paths, events)
		}
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

func TestLoginEnvSkipsWhatChangesEachLogin(t *testing.T) {
	raw := []byte("PATH=/usr/bin\x00SSH_AUTH_SOCK=/var/run/old/Listeners\x00TMPDIR=/var/t/\x00WINDOWID=7\x00TERMINFO=/k\x00STARSHIP_SESSION_KEY=k\x00COLORTERM=truecolor\x00")
	env := ParseEnv0(raw)
	if len(env) != 2 || env["PATH"] != "/usr/bin" || env["COLORTERM"] != "truecolor" {
		t.Errorf("env = %v, want only PATH and COLORTERM", env)
	}
}

func TestWithLive(t *testing.T) {
	saved := LoginEnv{"PATH": "/usr/bin", "SSH_AUTH_SOCK": "/var/run/old/Listeners", "SECURITYSESSIONID": "1"}
	live := map[string]string{"SSH_AUTH_SOCK": "/var/run/new/Listeners", "TMPDIR": "/var/t/"}
	got := saved.WithLive(func(k string) (string, bool) { v, ok := live[k]; return v, ok })
	want := LoginEnv{"PATH": "/usr/bin", "SSH_AUTH_SOCK": "/var/run/new/Listeners", "TMPDIR": "/var/t/"}
	if strings.Join(got.Environ(), ";") != strings.Join(want.Environ(), ";") {
		t.Errorf("got %v, want %v", got, want)
	}
	if saved["SSH_AUTH_SOCK"] != "/var/run/old/Listeners" {
		t.Error("WithLive changed the saved env")
	}
}
