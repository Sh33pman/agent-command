package harness

import (
	"reflect"
	"testing"
)

var _ Harness = Terminal{}

func TestTerminal(t *testing.T) {
	h := Terminal{Shell: "/bin/zsh"}
	want := []string{"/bin/zsh", "-l"}
	if got := h.Command(Spec{Prompt: "ignored", Args: []string{"ignored"}}); !reflect.DeepEqual(got, want) {
		t.Errorf("Command = %v", got)
	}
	if got := h.Resume("ignored", nil); !reflect.DeepEqual(got, want) {
		t.Errorf("Resume = %v", got)
	}
	if h.StatusSource() != StatusProcess || h.StatusSource().String() != "process" {
		t.Errorf("StatusSource = %v", h.StatusSource())
	}
}
