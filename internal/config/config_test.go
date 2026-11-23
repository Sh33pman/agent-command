package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTemplateIsDefault(t *testing.T) {
	c, err := Parse([]byte(Template))
	if err != nil {
		t.Fatal(err)
	}
	d := Default()
	if c.DefaultHarness != "" || c.Editor.RunIn != d.Editor.RunIn || c.Editor.Target != d.Editor.Target ||
		c.Editor.Preset != "" || len(c.Editor.Command) != 0 || c.Notify != d.Notify || c.Speech != d.Speech ||
		c.AgentRemoteControl || !reflect.DeepEqual(c.Remote, d.Remote) {
		t.Errorf("template %+v differs from defaults %+v", c, d)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	c, err := Load(filepath.Join(dir, "missing.toml"))
	if err != nil || c.Notify != Default().Notify {
		t.Fatalf("missing file: %+v %v", c, err)
	}
	p := filepath.Join(dir, "config.toml")
	must(t, os.WriteFile(p, []byte("future_key = 1\n[editor]\npreset = \"zed\"\n[notify]\ncooldown = \"1m\"\n"), 0o600))
	c, err = Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Editor.Preset != "zed" || c.Notify.Cooldown != "1m" || !c.Notify.OnDone || c.Editor.RunIn != "background" {
		t.Errorf("partial file not merged over defaults: %+v", c)
	}
	must(t, os.WriteFile(p, []byte("[editor\n"), 0o600))
	if _, err := Load(p); err == nil {
		t.Error("malformed file accepted")
	}
}

func TestSet(t *testing.T) {
	const user = `# my notes
claude_command = "claude"

[editor]
# keep me
preset = ""   # trailing
command = [
  "old",
]
target = "cwd"

[keys]
toggle = "cmd+b"
`
	cases := []struct {
		name, section, key string
		value              any
		want               string // substring of the result
	}{
		{"replace top level", "", "claude_command", "/opt/homebrew/bin/claude", "\nclaude_command = \"/opt/homebrew/bin/claude\"\n\n[editor]"},
		{"replace in section", "editor", "preset", "zed", "# keep me\npreset = \"zed\"\ncommand = ["},
		{"replace multi-line array", "editor", "command", []string{"nvim", "{dir}"}, "preset = \"\"   # trailing\ncommand = [\"nvim\", \"{dir}\"]\ntarget = \"cwd\"\n"},
		{"add to section", "editor", "resolved", "/usr/local/bin/code", "target = \"cwd\"\nresolved = \"/usr/local/bin/code\"\n\n[keys]"},
		{"add to top level", "", "claude_args", []string(nil), "claude_command = \"claude\"\nclaude_args = []\n\n[editor]"},
		{"add section", "view", "follow_new", false, "toggle = \"cmd+b\"\n\n[view]\nfollow_new = false\n"},
		{"quote special characters", "editor", "preset", `a"b\c`, `preset = "a\"b\\c"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Set(user, c.section, c.key, c.value)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(got, c.want) {
				t.Errorf("result lacks %q:\n%s", c.want, got)
			}
			if !strings.HasPrefix(got, "# my notes\n") {
				t.Errorf("leading comment lost:\n%s", got)
			}
		})
	}
	// Idempotent.
	once, err := Set(user, "editor", "preset", "zed")
	must(t, err)
	twice, err := Set(once, "editor", "preset", "zed")
	must(t, err)
	if once != twice {
		t.Errorf("not idempotent:\n%s\n---\n%s", once, twice)
	}
	// Empty file and a file without a trailing newline.
	got, err := Set("", "editor", "preset", "zed")
	must(t, err)
	if got != "[editor]\npreset = \"zed\"\n" {
		t.Errorf("empty file: %q", got)
	}
	got, err = Set("a = 1", "", "b", 2)
	must(t, err)
	if got != "a = 1\nb = 2\n" {
		t.Errorf("no trailing newline: %q", got)
	}
	// A key that isn't a table can't take a sub-key.
	if _, err := Set("editor = 1\n", "editor", "preset", "zed"); err == nil {
		t.Error("expected an error when [editor] would clash with a key")
	}
}

func TestSetTemplateRoundTrip(t *testing.T) {
	text := Template
	var err error
	for _, kv := range []struct {
		section, key string
		value        any
	}{
		{"", "default_harness", "01HARNESS"},
		{"editor", "preset", "custom"},
		{"editor", "command", []string{"open", "-a", "Sublime Text", "{dir}"}},
		{"editor", "run_in", "tab"},
	} {
		if text, err = Set(text, kv.section, kv.key, kv.value); err != nil {
			t.Fatal(err)
		}
	}
	c, err := Parse([]byte(text))
	must(t, err)
	if c.DefaultHarness != "01HARNESS" || c.Editor.Preset != "custom" ||
		strings.Join(c.Editor.Command, "|") != "open|-a|Sublime Text|{dir}" || c.Editor.RunIn != "tab" {
		t.Errorf("round trip: %+v", c)
	}
	if strings.Count(text, "\n") != strings.Count(Template, "\n") {
		t.Errorf("line count changed:\n%s", text)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestRetireTakesOutV1Settings(t *testing.T) {
	const v1 = `# mine
claude_command = "/bin/claude"
sidebar_width = 32
claude_args = [
  "--x",
]

[editor]
preset = "zed"

[keys]
# Global terminal shortcuts.
toggle = "cmd+shift+b"

[popup]
enabled = false

[popup.keys]
prev = "cmd+ctrl+left"

[speech]
sidebar_width = 1 # not top level: kept
voice = "af_heart"
`
	got, err := Retire(v1)
	if err != nil {
		t.Fatal(err)
	}
	const want = `# mine
claude_command = "/bin/claude"
claude_args = [
  "--x",
]

[editor]
preset = "zed"

[speech]
sidebar_width = 1 # not top level: kept
voice = "af_heart"
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if again, err := Retire(got); err != nil || again != got {
		t.Errorf("a second time changed it: %v\n%s", err, again)
	}
	if same, err := Retire(Template); err != nil || same != Template {
		t.Errorf("the template has nothing to retire: %v", err)
	}
	if out, err := Retire("[keys]\na = 1\n"); err != nil || out != "" {
		t.Errorf("only retired: %q %v", out, err)
	}
}

// Harness settings move into harnesses (the daemon's harnesses table): what
// config.toml had is read once, then taken out with the comments above it.
func TestOldHarnessSettingsMoveOut(t *testing.T) {
	const old = `# acmd
agent_remote_control = false

# Absolute path of the claude binary.
claude_command = "/opt/homebrew/bin/claude"
# Extra arguments for new sessions.
claude_args = [
  "--model", "opus",
]
agy_command = "agy"
agy_args = []
default_harness = "antigravity"

[editor]
preset = "zed"
`
	got, ok := ReadOldHarnesses(old)
	if !ok || got.ClaudeCommand != "/opt/homebrew/bin/claude" || strings.Join(got.ClaudeArgs, " ") != "--model opus" ||
		got.AgyCommand != "agy" || got.DefaultHarness != "antigravity" {
		t.Errorf("read %+v %v", got, ok)
	}
	out, err := RetireHarnessKeys(old)
	must(t, err)
	const want = `# acmd
agent_remote_control = false

default_harness = "antigravity"

[editor]
preset = "zed"
`
	if out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
	if left, ok := ReadOldHarnesses(out); !ok || left.ClaudeCommand != "" || left.ClaudeArgs != nil || left.DefaultHarness != "antigravity" {
		t.Errorf("a default_harness naming a kind is left for the daemon to make an id: %+v", left)
	}
	if _, ok := ReadOldHarnesses(`default_harness = "01ABC"` + "\n"); ok {
		t.Error("a harness id is nothing to move")
	}
	if same, err := Retire(old); err != nil || !strings.Contains(same, "claude_command") {
		t.Errorf("acmd init's Retire leaves them for the daemon: %v", err)
	}
}
