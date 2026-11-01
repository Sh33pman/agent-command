package harness

import (
	"reflect"
	"testing"
)

var _ Harness = Claude{}

func TestClaudeCommand(t *testing.T) {
	c := Claude{Path: "/bin/claude"}
	for _, tc := range []struct {
		name, prompt string
		args, want   []string
	}{
		{"", "", nil, []string{"/bin/claude", "--remote-control"}},
		{"fix", "", nil, []string{"/bin/claude", "--remote-control", "--name", "fix"}},
		{"fix", "do it", []string{"--model", "opus"}, []string{"/bin/claude", "--remote-control", "--name", "fix", "--model", "opus", "--", "do it"}},
		{"", "-x", nil, []string{"/bin/claude", "--remote-control", "--", "-x"}},
		{"", "", []string{"--remote-control=ci"}, []string{"/bin/claude", "--remote-control=ci"}},
	} {
		if got := c.Command("/w", tc.name, tc.prompt, tc.args); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Command(%q,%q,%v) = %v, want %v", tc.name, tc.prompt, tc.args, got, tc.want)
		}
	}
}

func TestClaudeResume(t *testing.T) {
	c := Claude{Path: "/bin/claude"}
	if got, want := c.Resume("abc", nil), []string{"/bin/claude", "--remote-control", "--resume", "abc"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Resume = %v, want %v", got, want)
	}
	if got, want := c.Resume("abc", []string{"--remote-control"}), []string{"/bin/claude", "--remote-control", "--resume", "abc"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Resume with flag = %v, want %v", got, want)
	}
}

func TestClaudeMeta(t *testing.T) {
	c := Claude{}
	if c.Name() != "claude" || c.StatusSource() != StatusHooks || StatusHooks.String() != "hooks" || StatusPTY.String() != "pty" {
		t.Error("unexpected metadata")
	}
}
