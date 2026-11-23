package setup

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAddAntigravityHooks(t *testing.T) {
	in := fixture(t, "agy-hooks-user.json")
	out, changed, err := AddAntigravityHooks(in, acmdPath)
	if err != nil || !changed {
		t.Fatalf("AddAntigravityHooks: changed=%v err=%v", changed, err)
	}
	golden(t, "agy-hooks-user.added.json", out)
	if got := strings.Join(keyOrder(t, out), ","); got != "lint-checker,reminder,agent-command" {
		t.Errorf("named hooks: %s", got)
	}
	// The other named hooks are kept as they were.
	var before, after map[string]json.RawMessage
	must(t, json.Unmarshal(in, &before))
	must(t, json.Unmarshal(out, &after))
	for name, v := range before {
		if !sameJSON(v, after[name]) {
			t.Errorf("%s changed: %s", name, after[name])
		}
	}
	// Flat lists for the plain events; a matcher group for PostToolUse.
	var ours map[string][]map[string]any
	must(t, json.Unmarshal(after[AntigravityHookName], &ours))
	if len(ours) != len(AntigravityEvents) || ours["Stop"][0]["command"] != AntigravityHookCommand(acmdPath, "Stop") ||
		ours["PostToolUse"][0]["matcher"] != "*" || ours["PostToolUse"][0]["hooks"] == nil || ours["PreToolUse"] != nil {
		t.Errorf("acmd's hook: %s", after[AntigravityHookName])
	}
	if st := AntigravityHooks(out); st.Events != len(AntigravityEvents) || len(st.Paths) != 1 || st.Paths[0] != acmdPath || st.Others != 2 || st.Disabled {
		t.Errorf("state %+v", st)
	}

	// Idempotent; a new binary path replaces the old one.
	again, changed, err := AddAntigravityHooks(out, acmdPath)
	if err != nil || changed || !bytes.Equal(again, out) {
		t.Errorf("second add changed the file: changed=%v err=%v", changed, err)
	}
	moved, _, err := AddAntigravityHooks(out, "/usr/local/bin/acmd")
	must(t, err)
	if st := AntigravityHooks(moved); len(st.Paths) != 1 || st.Paths[0] != "/usr/local/bin/acmd" {
		t.Errorf("moved: %+v", st)
	}

	// Remove restores the original, byte for byte (it's in agy's 2-space style).
	back, changed, err := RemoveAntigravityHooks(out)
	if err != nil || !changed || !bytes.Equal(back, in) {
		t.Errorf("remove: changed=%v err=%v\n%s", changed, err, back)
	}
	if same, changed, err := RemoveAntigravityHooks(in); err != nil || changed || !bytes.Equal(same, in) {
		t.Errorf("remove without ours: changed=%v err=%v", changed, err)
	}
}

func TestAntigravityHooksEdgeCases(t *testing.T) {
	// No file, an empty one, or one with a byte order mark.
	for _, in := range []string{"", "  \n", "\xef\xbb\xbf{}"} {
		out, changed, err := AddAntigravityHooks([]byte(in), acmdPath)
		if err != nil || !changed || AntigravityHooks(out).Events != len(AntigravityEvents) {
			t.Errorf("%q: changed=%v err=%v", in, changed, err)
		}
		back, _, err := RemoveAntigravityHooks(out)
		if err != nil || string(back) != "{}\n" {
			t.Errorf("%q: removed to %q, %v", in, back, err)
		}
	}
	// A hook of acmd's name that acmd didn't write is left alone.
	theirs := []byte(`{"agent-command": {"Stop": [{"command": "./mine.sh"}]}}`)
	if _, _, err := AddAntigravityHooks(theirs, acmdPath); err == nil {
		t.Error("overwrote someone else's agent-command hook")
	}
	if out, changed, err := RemoveAntigravityHooks(theirs); err != nil || changed || !bytes.Equal(out, theirs) {
		t.Errorf("removed someone else's hook: %v %v", changed, err)
	}
	for _, bad := range []string{"{", "[]", `{"a":1} x`} {
		if _, _, err := AddAntigravityHooks([]byte(bad), acmdPath); err == nil {
			t.Errorf("%q: no error", bad)
		}
		if _, _, err := RemoveAntigravityHooks([]byte(bad)); err == nil {
			t.Errorf("remove %q: no error", bad)
		}
		if st := AntigravityHooks([]byte(bad)); st.Events != 0 {
			t.Errorf("%q: %+v", bad, st)
		}
	}
	// Turned off in agy, or only partly there.
	off := []byte(`{"agent-command": {"enabled": false, "Stop": [{"command": "` + AntigravityHookCommand(acmdPath, "Stop") + `"}],
		"PostToolUse": "not a list"}}`)
	if st := AntigravityHooks(off); !st.Disabled || st.Events != 1 || st.Others != 0 {
		t.Errorf("disabled: %+v", st)
	}
	if st := AntigravityHooks([]byte(`{"agent-command": 3}`)); st.Events != 0 {
		t.Errorf("not an object: %+v", st)
	}
}

// The command must always print a JSON answer, even with acmd gone.
func TestAntigravityHookCommand(t *testing.T) {
	if got := AntigravityHookCommand("/Applications/Agent Command.app/Contents/MacOS/acmd", "Stop"); !strings.Contains(got, `'/Applications/Agent Command.app/Contents/MacOS/acmd' hook antigravity:Stop`) ||
		!strings.HasSuffix(got, "|| echo '{}' "+HookMarker) {
		t.Errorf("command %q", got)
	}
	out, err := exec.Command("/bin/sh", "-c", AntigravityHookCommand("/nonexistent/acmd", "Stop")).Output()
	if err != nil || string(out) != "{}\n" {
		t.Errorf("acmd gone: %q, %v", out, err)
	}
	// A stand-in acmd that answers once: the guard adds nothing.
	dir := t.TempDir()
	fake := filepath.Join(dir, "acmd")
	must(t, os.WriteFile(fake, []byte("#!/bin/sh\necho \"$@\" >&2\necho '{}'\n"), 0o700))
	cmd := exec.Command("/bin/sh", "-c", AntigravityHookCommand(fake, "Stop"))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err = cmd.Output()
	if err != nil || string(out) != "{}\n" || strings.TrimSpace(stderr.String()) != "hook antigravity:Stop" {
		t.Errorf("with acmd: %q %q %v", out, stderr.String(), err)
	}
}

func TestInitUninstallAntigravity(t *testing.T) {
	in, _ := fakeHome(t)
	in.AntigravityDir = AntigravityConfigDir(in.Home)
	in.AgyPath = "/Users/me/.local/bin/agy"
	user := read(t, "agy-hooks-user.json")
	must(t, os.MkdirAll(in.AntigravityDir, 0o755))
	must(t, os.WriteFile(in.AntigravityHooksFile(), user, 0o600))
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	backups := filepath.Join(in.Home, "backups")

	plan, err := PlanInit(in)
	must(t, err)
	if got := actions(plan)["hooks.json"]; got != "modify" {
		t.Errorf("hooks.json: %s", got)
	}
	var out bytes.Buffer
	plan.Write(&out, in.Home)
	if !strings.Contains(out.String(), `~/.gemini/config/hooks.json`) ||
		!strings.Contains(out.String(), `register acmd's Antigravity hooks for 4 events, as the hook "agent-command"; your 2 other named hooks are kept`) {
		t.Errorf("plan output:\n%s", out.String())
	}
	bdir, err := plan.Apply(backups, in.Home, now)
	must(t, err)
	if b, err := os.ReadFile(filepath.Join(bdir, ".gemini/config/hooks.json")); err != nil || !bytes.Equal(b, user) {
		t.Errorf("backup: %v", err)
	}
	if fi, err := os.Stat(in.AntigravityHooksFile()); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("permissions not kept: %v", err)
	}
	b, _ := os.ReadFile(in.AntigravityHooksFile())
	if AntigravityHooks(b).Events != len(AntigravityEvents) {
		t.Errorf("not registered:\n%s", b)
	}
	if again, _ := PlanInit(in); len(again.Pending()) != 0 {
		t.Errorf("second init: %v", actions(again))
	}

	// agy removed since: init leaves its file alone, uninstall still cleans it.
	in.AgyPath = ""
	if p, _ := PlanInit(in); actions(p)["hooks.json"] != "" {
		t.Errorf("init without agy touched hooks.json: %v", actions(p))
	}
	un, err := PlanUninstall(in)
	must(t, err)
	if got := actions(un)["hooks.json"]; got != "modify" {
		t.Errorf("uninstall hooks.json: %s", got)
	}
	out.Reset()
	un.Write(&out, in.Home)
	if !strings.Contains(out.String(), `remove acmd's Antigravity hook "agent-command"; your 2 other named hooks are kept`) {
		t.Errorf("uninstall output:\n%s", out.String())
	}
	_, err = un.Apply(backups, in.Home, now)
	must(t, err)
	if b, _ := os.ReadFile(in.AntigravityHooksFile()); !bytes.Equal(b, user) {
		t.Errorf("not restored:\n%s", b)
	}
	if un2, _ := PlanUninstall(in); len(un2.Pending()) != 0 {
		t.Errorf("second uninstall: %v", actions(un2))
	}
}

func TestInitAntigravityFreshAndBroken(t *testing.T) {
	in, _ := fakeHome(t)
	in.AntigravityDir = AntigravityConfigDir(in.Home)
	in.AgyPath = "/bin/agy"
	plan, err := PlanInit(in)
	must(t, err)
	if got := actions(plan)["hooks.json"]; got != "create" {
		t.Errorf("fresh: %s", got)
	}
	_, err = plan.Apply(filepath.Join(in.Home, "backups"), in.Home, time.Now())
	must(t, err)
	if fi, err := os.Stat(in.AntigravityHooksFile()); err != nil || fi.Mode().Perm() != 0o644 {
		t.Errorf("created: %v", err)
	}

	// A file acmd can't read is a note, and the rest of init goes ahead.
	must(t, os.WriteFile(in.AntigravityHooksFile(), []byte("{"), 0o600))
	plan, err = PlanInit(in)
	must(t, err)
	if _, ok := actions(plan)["hooks.json"]; ok || !strings.Contains(strings.Join(plan.Notes, "\n"), "~/.gemini/config/hooks.json was left alone") {
		t.Errorf("broken file: %v %v", actions(plan), plan.Notes)
	}
	if un, err := PlanUninstall(in); err != nil || actions(un)["hooks.json"] != "" || len(un.Notes) != 1 {
		t.Errorf("uninstall of a broken file: %v %v %v", actions(un), un.Notes, err)
	}

	// No agy dir in the input (older callers): nothing.
	in.AntigravityDir = ""
	if p, _ := PlanUninstall(in); actions(p)["hooks.json"] != "" {
		t.Error("touched hooks.json without a dir")
	}
}
