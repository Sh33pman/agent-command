package cli

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/setup"
)

// The OS's own notification, for a notice no client shows (the daemon's
// Config.Fallback): terminal-notifier or osascript on macOS, notify-send on a
// Linux desktop, and a toast on Windows. A machine with no desktop shows
// none: the notice still reaches terminal clients, or another machine.

// notifyEnv is what choosing a notifier looks at.
type notifyEnv struct {
	goos   string
	getenv func(string) string
	// look finds a program on the login PATH.
	look func(string) (string, error)
}

// notifyCommand is the program and arguments that show n here, or "" when
// there's no way to.
func notifyCommand(e notifyEnv, n ipc.Notice) (string, []string) {
	body := n.Body
	if n.Host != "" {
		body = strings.TrimSpace(body + " (on " + n.Host + ")")
	}
	switch e.goos {
	case "darwin":
		if tn, err := e.look("terminal-notifier"); err == nil {
			args := []string{"-title", n.Title, "-message", body, "-group", n.SessionID}
			if n.Sound != "" {
				args = append(args, "-sound", n.Sound)
			}
			return tn, args
		}
		sound := n.Sound
		if sound == "default" {
			sound = "Glass"
		}
		return "osascript", []string{"-e", osascriptNotify, n.Title, body, sound}
	case "windows":
		ps := filepath.Join(e.getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
		if e.getenv("SystemRoot") == "" {
			var err error
			if ps, err = e.look("powershell.exe"); err != nil {
				return "", nil
			}
		}
		return ps, []string{"-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-EncodedCommand", toastCommand(n.Title, body)}
	case "linux":
		// Only with a desktop session to show it: a headless box stays quiet.
		if e.getenv("DBUS_SESSION_BUS_ADDRESS") == "" && e.getenv("WAYLAND_DISPLAY") == "" && e.getenv("DISPLAY") == "" {
			return "", nil
		}
		ns, err := e.look("notify-send")
		if err != nil {
			return "", nil
		}
		return ns, []string{"--app-name=Agent Command", "--", n.Title, body}
	}
	return "", nil
}

const osascriptNotify = `on run argv
	if item 3 of argv is "" then
		display notification (item 2 of argv) with title (item 1 of argv)
	else
		display notification (item 2 of argv) with title (item 1 of argv) sound name (item 3 of argv)
	end if
end run`

// powershellAppID is Windows PowerShell's own AppUserModelID: a toast names
// an app, and acmd registers none of its own.
const powershellAppID = `{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe`

// toastCommand is a PowerShell script that shows a toast with title and body,
// for -EncodedCommand (base64 UTF-16LE). Title and body go in as base64 too,
// so nothing in them is read as PowerShell.
func toastCommand(title, body string) string {
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	script := `$ErrorActionPreference = 'Stop'
$d = { param($s) [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($s)) }
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
$x = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02)
$t = $x.GetElementsByTagName('text')
$t.Item(0).AppendChild($x.CreateTextNode((& $d '` + b64(title) + `'))) | Out-Null
$t.Item(1).AppendChild($x.CreateTextNode((& $d '` + b64(body) + `'))) | Out-Null
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('` + powershellAppID + `').Show([Windows.UI.Notifications.ToastNotification]::new($x))
`
	u := utf16.Encode([]rune(script))
	b := make([]byte, 2*len(u))
	for i, r := range u {
		binary.LittleEndian.PutUint16(b[2*i:], r)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// fallbackNotify shows n as the OS's notification, where there's one.
func fallbackNotify(n ipc.Notice) {
	name, args := notifyCommand(notifyEnv{goos: runtime.GOOS, getenv: os.Getenv,
		look: func(p string) (string, error) { return setup.LookPathIn(p, loginPath()) }}, n)
	if name == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, name, args...).Run() // #nosec G204 -- a notifier acmd chose; argv only
}
