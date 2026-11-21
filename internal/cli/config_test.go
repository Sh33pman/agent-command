package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/ipc"
)

func TestConfigShowAndSet(t *testing.T) {
	p := daemonEnv(t)
	file := p.Config()

	var view ipc.ConfigState
	must(t, json.Unmarshal([]byte(mustRun(t, "config", "show", "--json")), &view))
	if view.Path != file || view.Values["notify.cooldown"] != "20s" || view.Values["agent_remote_control"] != false ||
		view.Values["default_harness"] != "" || view.Values["editor.preset"] != "" || len(view.Settings) != len(config.Specs) {
		t.Fatalf("defaults: %+v", view)
	}
	for _, s := range view.Settings {
		if s.Key == "speech.voice" && len(s.Suggestions) != 20 {
			t.Errorf("voices: %v", s.Suggestions)
		}
	}
	if out := mustRun(t, "config", "path"); strings.TrimSpace(out) != file {
		t.Errorf("path: %q", out)
	}

	for _, c := range [][2]string{
		{"notify.cooldown", "90s"}, {"notify.on_paused", "on"}, {"notify.sound", ""}, {"agent_remote_control", "true"},
		{"claude_args", `["--model", "opus"]`}, {"speech.voice", "am_adam:0.5,af_sky:0.5"}, {"speech.speed", "1.25"},
		{"editor.target", "git_root"}, {"claude_command", " /usr/local/bin/claude "},
		{"agy_command", "/opt/agy/bin/agy"}, {"agy_args", `["--mode", "plan"]`}, {"default_harness", "antigravity"},
	} {
		mustRun(t, "config", "set", c[0], c[1])
	}
	cfg, err := config.Load(file)
	must(t, err)
	want := config.Notify{Enabled: true, OnWaiting: true, OnError: true, OnDone: true, OnPaused: true,
		DoneMinDuration: "10s", Cooldown: "1m30s", Sound: ""}
	if cfg.Notify != want || !cfg.AgentRemoteControl || strings.Join(cfg.ClaudeArgs, " ") != "--model opus" ||
		cfg.Speech.Voice != "am_adam:0.5,af_sky:0.5" || cfg.Speech.Speed != 1.25 || cfg.Editor.Target != "git_root" ||
		cfg.ClaudeCommand != "/usr/local/bin/claude" || cfg.AgyCommand != "/opt/agy/bin/agy" || strings.Join(cfg.AgyArgs, " ") != "--mode plan" ||
		cfg.DefaultHarness != "antigravity" {
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
		{"claude_args", "[1"}, {"agy_args", "[1"}, {"agy_command", " "}, {"editor.preset", "notepad"}, {"nope", "1"}, {"notify.sound", "ping"},
		{"editor.resolved", "/x"},
		{"default_harness", "terminal"}, {"default_harness", "copilot"},
	} {
		if _, err := acmd(t, "config", "set", bad[0], bad[1]); err == nil {
			t.Errorf("%s = %q accepted", bad[0], bad[1])
		}
	}
	// Several at once: all, or none.
	if out := mustRun(t, "config", "set", "notify.on_done", "off", "remote_control", "off"); out != "notify.on_done = false\nagent_remote_control = false\n" {
		t.Errorf("two: %q", out)
	}
	if _, err := acmd(t, "config", "set", "notify.on_done", "on", "notify.cooldown", "soon"); err == nil {
		t.Error("a bad pair")
	}
	if c, _ := config.Load(file); c.Notify.OnDone {
		t.Error("half of a refused change was made")
	}
	if _, err := acmd(t, "config", "set", "notify.on_done"); err == nil {
		t.Error("a setting with no value")
	}
	// A mistake by hand: show says so, and set waits for the fix.
	b0, _ := os.ReadFile(file)
	must(t, os.WriteFile(file, append(b0, []byte("\nbroken = \n")...), 0o600))
	later := time.Now().Add(time.Minute)
	must(t, os.Chtimes(file, later, later))
	if out := mustRun(t, "config", "show"); !strings.Contains(out, "these are the last good settings") {
		t.Errorf("show with a mistake:\n%s", out)
	}
	if _, err := acmd(t, "config", "set", "notify.on_done", "on"); err == nil || !strings.Contains(err.Error(), "fix it first") {
		t.Errorf("set with a mistake: %v", err)
	}
	must(t, os.WriteFile(file, b0, 0o600))
	b, err := os.ReadFile(file)
	must(t, err)
	if !strings.Contains(string(b), "# Notifications") {
		t.Errorf("a missing file starts from the template:\n%s", b)
	}
}
