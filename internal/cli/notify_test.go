package cli

import (
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/brettmostert/agent-command/internal/ipc"
)

// fakeNotifyEnv is goos with env set and only programs found.
func fakeNotifyEnv(goos string, env map[string]string, programs ...string) notifyEnv {
	return notifyEnv{goos: goos, getenv: func(k string) string { return env[k] },
		look: func(p string) (string, error) {
			for _, have := range programs {
				if have == p {
					return "/bin/" + p, nil
				}
			}
			return "", errors.New("not found")
		}}
}

// decodeToast is the PowerShell script -EncodedCommand carries.
func decodeToast(t *testing.T, enc string) string {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || len(b)%2 != 0 {
		t.Fatalf("not base64 UTF-16: %v", err)
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return string(utf16.Decode(u))
}

func TestNotifyCommandOnEachOS(t *testing.T) {
	n := ipc.Notice{SessionID: "s1", Title: "⚠ api needs you", Body: "Bash", Sound: "default", Host: "studio"}
	desktop := map[string]string{"DBUS_SESSION_BUS_ADDRESS": "unix:path=/run/user/1000/bus"}
	for _, c := range []struct {
		name string
		e    notifyEnv
		want string // the program and arguments, joined by |; "" for none
	}{
		{"macOS with terminal-notifier", fakeNotifyEnv("darwin", nil, "terminal-notifier"),
			"/bin/terminal-notifier|-title|⚠ api needs you|-message|Bash (on studio)|-group|s1|-sound|default"},
		{"macOS without it", fakeNotifyEnv("darwin", nil), "osascript|-e|" + osascriptNotify + "|⚠ api needs you|Bash (on studio)|Glass"},
		{"a Linux desktop", fakeNotifyEnv("linux", desktop, "notify-send"),
			"/bin/notify-send|--app-name=Agent Command|--|⚠ api needs you|Bash (on studio)"},
		{"a Linux desktop on Wayland", fakeNotifyEnv("linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, "notify-send"),
			"/bin/notify-send|--app-name=Agent Command|--|⚠ api needs you|Bash (on studio)"},
		{"a headless Linux box", fakeNotifyEnv("linux", nil, "notify-send"), ""},
		{"a Linux desktop without notify-send", fakeNotifyEnv("linux", desktop), ""},
		{"Windows without SystemRoot or PowerShell", fakeNotifyEnv("windows", nil), ""},
		{"FreeBSD", fakeNotifyEnv("freebsd", desktop, "notify-send"), ""},
	} {
		name, args := notifyCommand(c.e, n)
		got := ""
		if name != "" {
			got = strings.Join(append([]string{name}, args...), "|")
		}
		if got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}

	name, args := notifyCommand(fakeNotifyEnv("windows", map[string]string{"SystemRoot": `C:\Windows`}), n)
	if !strings.HasSuffix(name, "powershell.exe") || !strings.Contains(name, "WindowsPowerShell") ||
		strings.Join(args[:5], " ") != "-NoProfile -NonInteractive -WindowStyle Hidden -EncodedCommand" {
		t.Fatalf("Windows: %s %v", name, args)
	}
	script := decodeToast(t, args[5])
	for _, want := range []string{"ToastNotificationManager", powershellAppID, "ToastText02",
		base64.StdEncoding.EncodeToString([]byte("⚠ api needs you")), base64.StdEncoding.EncodeToString([]byte("Bash (on studio)"))} {
		if !strings.Contains(script, want) {
			t.Errorf("the toast script lacks %q:\n%s", want, script)
		}
	}
	if name, _ := notifyCommand(fakeNotifyEnv("windows", nil, "powershell.exe"), n); name != "/bin/powershell.exe" {
		t.Errorf("Windows from PATH: %q", name)
	}
}

// Whatever a title or body holds, it's data, not PowerShell.
func TestToastTextIsNeverCode(t *testing.T) {
	evil := `'); Remove-Item C:\ -Recurse; ('`
	script := decodeToast(t, toastCommand(evil, evil))
	if strings.Contains(script, "Remove-Item") {
		t.Errorf("the text reached the script as code:\n%s", script)
	}
}

// On a Windows runner, the toast script runs in real PowerShell: its types
// load and it shows a toast, or Windows says why in the error.
func TestToastRunsInPowerShell(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("needs Windows PowerShell")
	}
	if os.Getenv("CI") != "true" && os.Getenv("ACMD_TEST_TOAST") != "1" {
		t.Skip("shows a real toast: set ACMD_TEST_TOAST=1 to run it here")
	}
	name, args := notifyCommand(notifyEnv{goos: "windows", getenv: os.Getenv, look: exec.LookPath},
		ipc.Notice{Title: "acmd test", Body: "a toast from the tests"})
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		t.Fatalf("the toast script failed: %v\n%s", err, out)
	}
}
