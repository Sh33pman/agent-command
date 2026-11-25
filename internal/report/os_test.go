package report

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

func TestOS(t *testing.T) {
	got := OS(context.Background())
	want := map[string]string{"darwin": "macOS ", "windows": "Windows "}[runtime.GOOS]
	if got == "" || !strings.HasPrefix(got, want) {
		t.Errorf("OS() = %q, want it to start %q", got, want)
	}
}

func TestOSRelease(t *testing.T) {
	for _, c := range []struct{ file, want string }{
		{"NAME=\"Ubuntu\"\nVERSION_ID=\"24.04\"\nPRETTY_NAME=\"Ubuntu 24.04.1 LTS\"\n", "Ubuntu 24.04.1 LTS"},
		{"# a comment\nNAME=Arch Linux\nID=arch\n", "Arch Linux"},
		{"NAME='Debian GNU/Linux'\nVERSION_ID=12\n", "Debian GNU/Linux 12"},
		{"PRETTY_NAME=\"Fedora \\\"Linux\\\" 41\"\n", `Fedora "Linux" 41`},
		{"", ""},
	} {
		if got := osRelease([]byte(c.file)); got != c.want {
			t.Errorf("osRelease(%q) = %q, want %q", c.file, got, c.want)
		}
	}
}
