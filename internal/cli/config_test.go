package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/config"
)

func TestConfigShowAndSet(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ACMD_CONFIG_DIR", dir)
	t.Setenv("ACMD_STATE_DIR", filepath.Join(dir, "s"))
	t.Setenv("ACMD_RUNTIME_DIR", filepath.Join(dir, "r"))
	file := filepath.Join(dir, "config.toml")

	var view configView
	must(t, json.Unmarshal([]byte(mustRun(t, "config", "show", "--json")), &view))
	if view.Path != file || view.Settings["notify.cooldown"] != "20s" || view.Settings["remote_control"] != false ||
		len(view.Voices) != 20 || view.Settings["editor.preset"] != "" {
		t.Fatalf("defaults: %+v", view)
	}
	if out := mustRun(t, "config", "path"); strings.TrimSpace(out) != file {
		t.Errorf("path: %q", out)
	}

	for _, c := range [][2]string{
		{"notify.cooldown", "90s"}, {"notify.on_paused", "on"}, {"notify.sound", ""}, {"remote_control", "true"},
		{"claude_args", `["--model", "opus"]`}, {"speech.voice", "am_adam:0.5,af_sky:0.5"}, {"speech.speed", "1.25"},
		{"editor.target", "git_root"}, {"claude_command", " /usr/local/bin/claude "},
	} {
		mustRun(t, "config", "set", c[0], c[1])
	}
	cfg, err := config.Load(file)
	must(t, err)
	want := config.Notify{Enabled: true, OnWaiting: true, OnError: true, OnDone: true, OnPaused: true,
		DoneMinDuration: "10s", Cooldown: "1m30s", Sound: ""}
	if cfg.Notify != want || !cfg.RemoteControl || strings.Join(cfg.ClaudeArgs, " ") != "--model opus" ||
		cfg.Speech.Voice != "am_adam:0.5,af_sky:0.5" || cfg.Speech.Speed != 1.25 || cfg.Editor.Target != "git_root" ||
		cfg.ClaudeCommand != "/usr/local/bin/claude" {
		t.Errorf("saved: %+v", cfg)
	}
	mustRun(t, "config", "set", "--", "claude_args", "--verbose  --x")
	mustRun(t, "config", "set", "claude_args", "")
	if cfg, _ = config.Load(file); len(cfg.ClaudeArgs) != 0 {
		t.Errorf("args: %q", cfg.ClaudeArgs)
	}

	out := mustRun(t, "config", "show")
	if !strings.Contains(out, `notify.cooldown = "1m30s"`) || !strings.Contains(out, "speech.speed = 1.25") {
		t.Errorf("show:\n%s", out)
	}

	for _, bad := range [][2]string{
		{"notify.cooldown", "soon"}, {"notify.cooldown", "-1s"}, {"notify.enabled", "maybe"}, {"speech.voice", "bf_emma"},
		{"speech.voice", " "}, {"speech.speed", "3"}, {"editor.run_in", "window"}, {"claude_command", ""},
		{"claude_args", "[1"}, {"editor.preset", "zed"}, {"nope", "1"}, {"notify.sound", "ping"},
	} {
		if _, err := acmd(t, "config", "set", bad[0], bad[1]); err == nil {
			t.Errorf("%s = %q accepted", bad[0], bad[1])
		}
	}
	if _, err := acmd(t, "config", "set", "editor.preset", "zed"); err == nil || !strings.Contains(err.Error(), "acmd editor set") {
		t.Errorf("preset: %v", err)
	}
	b, err := os.ReadFile(file)
	must(t, err)
	if !strings.Contains(string(b), "# Notifications") {
		t.Errorf("a missing file starts from the template:\n%s", b)
	}
}
