package procenv

import (
	"reflect"
	"testing"
)

func TestScrub(t *testing.T) {
	in := []string{
		"PATH=/usr/bin", "CLAUDECODE=1", "CLAUDE_CODE_SESSION_ID=x", "CLAUDE_CODE_CHILD_SESSION=1",
		"CLAUDE_PID=42", "CLAUDE_EFFORT=high", "HOME=/h", "MY_CLAUDE=keep", "KITTY_WINDOW_ID=3", "MALFORMED",
	}
	want := []string{"PATH=/usr/bin", "HOME=/h", "MY_CLAUDE=keep", "KITTY_WINDOW_ID=3", "MALFORMED"}
	if got := Scrub(in); !reflect.DeepEqual(got, want) {
		t.Errorf("Scrub = %v, want %v", got, want)
	}
	if got := Scrub(nil); len(got) != 0 {
		t.Errorf("Scrub(nil) = %v", got)
	}
}
