package harness

import (
	"slices"
	"strings"
	"testing"
)

func TestSettingsArgs(t *testing.T) {
	got := SettingsArgs(ClaudeSettings, map[string][]string{
		"add-dir":         {"/a", " ", "/b"},
		"model":           {" opus "},
		"permission-mode": {"acceptEdits"},
		"allowed-tools":   {"Bash(git log:*)", "Edit"},
		"effort":          {""},
	})
	want := []string{"--model", "opus", "--permission-mode", "acceptEdits",
		"--allowedTools", "Bash(git log:*)", "--allowedTools", "Edit", "--add-dir", "/a", "--add-dir", "/b"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if args := SettingsArgs(ClaudeSettings, nil); len(args) != 0 {
		t.Errorf("no settings: %q", args)
	}
}

func TestCheckSettings(t *testing.T) {
	ok := map[string][]string{"model": {"opus"}, "effort": {"high"}, "add-dir": {"/a", "/b"}, "append-system-prompt": {"- be brief"}}
	if err := CheckSettings(ClaudeSettings, ok); err != nil {
		t.Errorf("valid: %v", err)
	}
	for _, tc := range []struct {
		values map[string][]string
		want   string
	}{
		{map[string][]string{"flavor": {"red"}}, `no setting "flavor"`},
		{map[string][]string{"effort": {"huge"}}, "one of low, medium"},
		{map[string][]string{"model": {"opus", "sonnet"}}, "takes one value"},
		{map[string][]string{"model": {"--dangerously-skip-permissions"}}, "dash"},
		{map[string][]string{"add-dir": {"-x"}}, "dash"},
	} {
		if err := CheckSettings(ClaudeSettings, tc.values); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: got %v, want %q", tc.values, err, tc.want)
		}
	}
}

func TestSettingsFor(t *testing.T) {
	if len(SettingsFor("")) == 0 || len(SettingsFor("claude")) == 0 || SettingsFor("terminal") != nil {
		t.Error("settings by harness")
	}
	keys := map[string]bool{}
	for _, s := range ClaudeSettings {
		if keys[s.Key] || s.Label == "" || s.Flag == "" || !strings.HasPrefix(s.Flag, "--") {
			t.Errorf("setting %+v", s)
		}
		keys[s.Key] = true
		if s.Kind == KindChoice && len(s.Choices) == 0 {
			t.Errorf("%s has no choices", s.Key)
		}
	}
}
