package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"time"
)

// LoginEnv is the user's login-shell environment, captured at init so the
// processes acmd starts find `claude`, editors, etc. even when acmd was
// launched by a terminal keybinding with a minimal PATH (M0 K7).
type LoginEnv map[string]string

// excludedEnv are variables that belong to the moment of capture, not to
// the processes acmd starts later.
var excludedEnv = map[string]bool{
	"PWD": true, "OLDPWD": true, "SHLVL": true, "_": true, "TERM_SESSION_ID": true,
	"KITTY_WINDOW_ID": true, "KITTY_PID": true, "KITTY_LISTEN_ON": true, "KITTY_PUBLIC_KEY": true,
	"KITTY_INSTALLATION_DIR": true, "WINDOWID": true, "TERMINFO": true,
	"GHOSTTY_RESOURCES_DIR": true, "STARSHIP_SESSION_KEY": true,
}

// LiveEnv are variables that change with each login, such as the SSH agent's
// socket. They aren't saved: a saved one stops working after a restart. Sessions
// get them from the process starting them instead (WithLive).
var LiveEnv = []string{
	"SSH_AUTH_SOCK", "SECURITYSESSIONID", "LaunchInstanceID", "TMPDIR",
	"XPC_FLAGS", "XPC_SERVICE_NAME", "__CFBundleIdentifier",
	"DISPLAY", "WAYLAND_DISPLAY", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS",
}

// WithLive returns e with LiveEnv taken from getenv: set where it has them, and
// removed where it doesn't, so an env.json saved before this keeps no stale ones.
func (e LoginEnv) WithLive(getenv func(string) (string, bool)) LoginEnv {
	out := make(LoginEnv, len(e))
	for k, v := range e {
		out[k] = v
	}
	for _, k := range LiveEnv {
		if v, ok := getenv(k); ok {
			out[k] = v
		} else {
			delete(out, k)
		}
	}
	return out
}

// ParseEnv0 parses `env -0` output, dropping CLAUDE* (M0 C5) and
// capture-specific variables.
func ParseEnv0(out []byte) LoginEnv {
	env := LoginEnv{}
	for _, kv := range bytes.Split(out, []byte{0}) {
		k, v, ok := strings.Cut(string(kv), "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" || strings.HasPrefix(k, "CLAUDE") || excludedEnv[k] || slices.Contains(LiveEnv, k) {
			continue
		}
		env[k] = v
	}
	return env
}

// CaptureLoginEnv runs the user's login shell to read its environment.
func CaptureLoginEnv(ctx context.Context, shell string) (LoginEnv, error) {
	if shell == "" {
		shell = defaultShell()
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-l", "-c", "env -0") // #nosec G204 -- the user's own $SHELL
	cmd.Stdin = nil
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("capture login shell environment (%s -l): %w", shell, err)
	}
	env := ParseEnv0(out)
	if env["PATH"] == "" {
		return nil, fmt.Errorf("login shell %s reported no PATH", shell)
	}
	return env, nil
}

// Environ returns the environment as sorted KEY=VALUE pairs.
func (e LoginEnv) Environ() []string {
	out := make([]string, 0, len(e))
	for k, v := range e {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// MarshalFile renders the env as stable, indented JSON for env.json.
func (e LoginEnv) MarshalFile() ([]byte, error) {
	b, err := json.MarshalIndent(map[string]string(e), "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
