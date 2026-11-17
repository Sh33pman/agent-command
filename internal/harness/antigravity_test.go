package harness

import (
	"reflect"
	"slices"
	"testing"
)

var (
	_ Harness  = Antigravity{}
	_ Prompter = Antigravity{}
	_ Prompter = Claude{}
)

func TestAntigravityCommand(t *testing.T) {
	a := Antigravity{Path: "/bin/agy"}
	for _, tc := range []struct {
		sp   Spec
		want []string
	}{
		{Spec{}, []string{"/bin/agy"}},
		// Name and SessionID have no agy flag.
		{Spec{Dir: "/w", Name: "fix", SessionID: "u-1"}, []string{"/bin/agy"}},
		{Spec{Prompt: "fix the tests"}, []string{"/bin/agy", "-i=fix the tests"}},
		{Spec{Prompt: "-x"}, []string{"/bin/agy", "-i=-x"}},
		{Spec{Prompt: "go", Args: []string{"--model", "flash"}}, []string{"/bin/agy", "-i=go", "--model", "flash"}},
		{Spec{Args: []string{"--remote-control"}}, []string{"/bin/agy", "--remote-control"}},
	} {
		if got := a.Command(tc.sp); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Command(%+v) = %v, want %v", tc.sp, got, tc.want)
		}
	}
}

func TestAntigravityResume(t *testing.T) {
	a := Antigravity{Path: "/bin/agy"}
	if got, want := a.Resume("conv-1", nil), []string{"/bin/agy", "--conversation", "conv-1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Resume = %v, want %v", got, want)
	}
	if got, want := a.Resume("conv-1", []string{"--mode", "plan"}), []string{"/bin/agy", "--conversation", "conv-1", "--mode", "plan"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Resume with args = %v, want %v", got, want)
	}
}

func TestAntigravityMeta(t *testing.T) {
	a := Antigravity{}
	if a.Name() != "antigravity" || a.StatusSource() != StatusHooks {
		t.Error("unexpected metadata")
	}
	if got := (Claude{}).PromptArgs("-x"); !reflect.DeepEqual(got, []string{"--", "-x"}) {
		t.Errorf("Claude.PromptArgs = %v", got)
	}
}

// With the remote_control setting, agy starts and resumes with --remote-control,
// first, and not twice when the user's args already have it.
func TestAntigravityRemoteControl(t *testing.T) {
	a := Antigravity{Path: "/bin/agy", RemoteControl: true}
	if got, want := a.Command(Spec{Prompt: "hi"}), []string{"/bin/agy", "--remote-control", "-i=hi"}; !slices.Equal(got, want) {
		t.Errorf("Command = %q, want %q", got, want)
	}
	if got, want := a.Resume("c1", nil), []string{"/bin/agy", "--remote-control", "--conversation", "c1"}; !slices.Equal(got, want) {
		t.Errorf("Resume = %q, want %q", got, want)
	}
	if got, want := a.Command(Spec{Args: []string{"--remote-control"}}), []string{"/bin/agy", "--remote-control"}; !slices.Equal(got, want) {
		t.Errorf("Command with the flag in args = %q, want %q", got, want)
	}
	if got := (Antigravity{Path: "/bin/agy"}).Command(Spec{}); slices.Contains(got, "--remote-control") {
		t.Errorf("Remote Control off: %q", got)
	}
}
