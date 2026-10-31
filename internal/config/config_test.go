package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTemplateIsDefault(t *testing.T) {
	c, err := Parse([]byte(Template))
	if err != nil {
		t.Fatal(err)
	}
	d := Default()
	if c.ClaudeCommand != d.ClaudeCommand || c.Editor.RunIn != d.Editor.RunIn || c.Editor.Target != d.Editor.Target ||
		c.Editor.Preset != "" || len(c.Editor.Command) != 0 || c.Keys != d.Keys {
		t.Errorf("template %+v differs from defaults %+v", c, d)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	c, err := Load(filepath.Join(dir, "missing.toml"))
	if err != nil || c.Keys != Default().Keys {
		t.Fatalf("missing file: %+v %v", c, err)
	}
	p := filepath.Join(dir, "config.toml")
	must(t, os.WriteFile(p, []byte("future_key = 1\n[editor]\npreset = \"zed\"\n[keys]\ntoggle = \"cmd+b\"\n"), 0o600))
	c, err = Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Editor.Preset != "zed" || c.Keys.Toggle != "cmd+b" || c.Keys.New != "cmd+shift+n" || c.Editor.RunIn != "background" {
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
		{"", "claude_command", "/Users/me/.local/bin/claude"},
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
	if c.ClaudeCommand != "/Users/me/.local/bin/claude" || c.Editor.Preset != "custom" ||
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
