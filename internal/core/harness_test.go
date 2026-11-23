package core

import (
	"slices"
	"testing"
)

// A harness's name is letters with single spaces between words, checked as
// typed: nothing is cleaned up.
func TestHarnessNames(t *testing.T) {
	for _, ok := range []string{"Claude", "My Work", " Personal ", "a"} {
		if err := CheckHarnessName(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for name, why := range map[string]string{
		"": "needs a name", "   ": "needs a name", "Work2": "letters", "my-work": "letters",
		"My  Work": "single spaces", "Wörk": "letters", "work_x": "letters",
	} {
		if err := CheckHarnessName(name); err == nil {
			t.Errorf("%q accepted (%s)", name, why)
		}
	}
	if HarnessSlug(" My Work ") != "my_work" || HarnessSlug("my work") != HarnessSlug("MY WORK") {
		t.Error("slug: lower case, _ for spaces")
	}
	if DefaultConfigDir("My Work") != "~/.claude-my_work" {
		t.Errorf("folder = %s", DefaultConfigDir("My Work"))
	}
}

// A Claude harness with its own folder sets CLAUDE_CONFIG_DIR; one on
// Claude's standard folder leaves the environment as it is, so Claude keeps
// using ~/.claude.json and its usual sign-in.
func TestHarnessFolderAndEnv(t *testing.T) {
	env := []string{"PATH=/bin", "CLAUDE_CONFIG_DIR=/old"}
	getenv := func(string) string { return "" }
	work := Harness{Kind: HarnessClaude, ConfigDir: "~/.claude-work"}
	if got := work.Env(env, "/home/me"); !slices.Equal(got, []string{"PATH=/bin", "CLAUDE_CONFIG_DIR=/home/me/.claude-work"}) {
		t.Errorf("env = %v", got)
	}
	if work.ClaudeDir(getenv, "/home/me") != "/home/me/.claude-work" {
		t.Error("its own folder")
	}
	std := Harness{Kind: HarnessClaude}
	if got := std.Env(env, "/home/me"); !slices.Equal(got, env) {
		t.Errorf("standard folder changed env: %v", got)
	}
	if std.ClaudeDir(getenv, "/home/me") != "/home/me/.claude" ||
		std.ClaudeDir(func(string) string { return "/x" }, "/home/me") != "/x" {
		t.Error("standard folder: CLAUDE_CONFIG_DIR, else ~/.claude")
	}
	agy := Harness{Kind: HarnessAntigravity, ConfigDir: "~/ignored"}
	if got := agy.Env(env, "/home/me"); !slices.Equal(got, env) {
		t.Errorf("agy has no folder: %v", got)
	}
	if ExpandHome("~", "/h") != "/h" || ExpandHome("/abs", "/h") != "/abs" || ExpandHome("~x", "/h") != "~x" {
		t.Error("ExpandHome")
	}
	if agy.RunCommand() != "agy" || std.RunCommand() != "claude" || (Harness{Command: " /bin/c "}).RunCommand() != "/bin/c" {
		t.Error("RunCommand")
	}
	if KindTitle(HarnessTerminal) != "Terminal" || KindTitle("x") != "x" || !IsHarnessKind(HarnessAntigravity) || IsHarnessKind(HarnessTerminal) {
		t.Error("kinds")
	}
}
