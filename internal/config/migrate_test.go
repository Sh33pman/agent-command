package config

import (
	"strings"
	"testing"
)

func TestParseAgentRemoteControl(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bool
	}{
		{"", false},
		{"agent_remote_control = true\n", true},
		{"remote_control = true\n", true}, // the old name
		{"remote_control = false\n", false},
		{"agent_remote_control = false\nremote_control = true\n", false}, // the new name wins
		{"agent_remote_control = true\nremote_control = false\n", true},
		{"[speech]\nremote_control = true\n", false}, // only the top level counts
	} {
		c, err := Parse([]byte(tc.text))
		must(t, err)
		if c.AgentRemoteControl != tc.want {
			t.Errorf("%q: %v, want %v", tc.text, c.AgentRemoteControl, tc.want)
		}
	}
	if _, err := Parse([]byte("remote_control = 1\n")); err == nil {
		t.Error("a number accepted")
	}
}

func TestMigrate(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{Template, Template},
		{"# mine\n  remote_control=true # kept\n[notify]\nenabled = true\n", "# mine\n  agent_remote_control=true # kept\n[notify]\nenabled = true\n"},
		{"# remote_control = true\n", "# remote_control = true\n"},
		{"[speech]\nremote_control = true\n", "[speech]\nremote_control = true\n"},
		{"[[x]]\nremote_control = true\n", "[[x]]\nremote_control = true\n"},
		// Both: left as it is, for Retire to drop the old one.
		{"remote_control = true\nagent_remote_control = false\n", "remote_control = true\nagent_remote_control = false\n"},
	} {
		if got := Migrate(tc.in); got != tc.want {
			t.Errorf("Migrate(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRetireMigratesAgentRemoteControl(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"remote_control = true\n[notify]\nenabled = true\n", "agent_remote_control = true\n[notify]\nenabled = true\n"},
		{"agent_remote_control = false\nremote_control = true\n\n[notify]\nenabled = true\n", "agent_remote_control = false\n\n[notify]\nenabled = true\n"},
	} {
		got, err := Retire(tc.in)
		must(t, err)
		if got != tc.want {
			t.Errorf("Retire(%q) = %q, want %q", tc.in, got, tc.want)
		}
		c, err := Parse([]byte(got))
		must(t, err)
		if want := strings.HasPrefix(tc.want, "agent_remote_control = true"); c.AgentRemoteControl != want {
			t.Errorf("%q reads %v", got, c.AgentRemoteControl)
		}
	}
	// The template's [remote] is commented out and stays so.
	if !strings.Contains(Template, "\n# [remote]\n") || strings.Contains(Template, "\nremote_control") {
		t.Error("template: want agent_remote_control and a commented [remote]")
	}
}
