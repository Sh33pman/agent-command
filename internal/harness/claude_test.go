package harness

import (
	"reflect"
	"testing"
)

var _ Harness = Claude{}

func TestClaudeCommand(t *testing.T) {
	c := Claude{Path: "/bin/claude", RemoteControl: true}
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
		if got := c.Command(Spec{Dir: "/w", Name: tc.name, Prompt: tc.prompt, Args: tc.args}); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Command(%q,%q,%v) = %v, want %v", tc.name, tc.prompt, tc.args, got, tc.want)
		}
	}
}

func TestClaudeCommandSessionID(t *testing.T) {
	got := Claude{Path: "/c", RemoteControl: true}.Command(Spec{Name: "n", SessionID: "u-1", Prompt: "p"})
	want := []string{"/c", "--remote-control", "--name", "n", "--session-id", "u-1", "--", "p"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestClaudeResume(t *testing.T) {
	c := Claude{Path: "/bin/claude", RemoteControl: true}
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

func TestClaudeWithRemoteControlOff(t *testing.T) {
	c := Claude{Path: "/c"} // the default
	if got, want := c.Command(Spec{Name: "n", SessionID: "u", Prompt: "p"}), []string{"/c", "--name", "n", "--session-id", "u", "--", "p"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Command = %v, want %v", got, want)
	}
	if got, want := c.Resume("abc", nil), []string{"/c", "--resume", "abc"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Resume = %v, want %v", got, want)
	}
	// The user's own flag still counts.
	if got, want := c.Command(Spec{Args: []string{"--remote-control"}}), []string{"/c", "--remote-control"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Command with the flag = %v, want %v", got, want)
	}
}
