package setup

import (
	"context"
	"os"
	"testing"
)

func TestCaptureLoginEnvDefaultShell(t *testing.T) {
	env, err := CaptureLoginEnv(context.Background(), "")
	if err != nil {
		t.Fatalf("CaptureLoginEnv default shell: %v", err)
	}
	if env["PATH"] == "" {
		t.Error("expected non-empty PATH")
	}
}

func TestLiveEnvIncludesSessionVars(t *testing.T) {
	raw := []byte("PATH=/usr/bin\x00DISPLAY=:0\x00WAYLAND_DISPLAY=wayland-0\x00XDG_RUNTIME_DIR=/run/user/1000\x00DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus\x00")
	parsed := ParseEnv0(raw)
	for _, k := range []string{"DISPLAY", "WAYLAND_DISPLAY", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS"} {
		if _, ok := parsed[k]; ok {
			t.Errorf("ParseEnv0 should exclude live var %s", k)
		}
	}

	live := parsed.WithLive(func(k string) (string, bool) {
		if k == "WAYLAND_DISPLAY" {
			return "wayland-1", true
		}
		return "", false
	})
	if live["WAYLAND_DISPLAY"] != "wayland-1" {
		t.Errorf("WAYLAND_DISPLAY = %q, want wayland-1", live["WAYLAND_DISPLAY"])
	}
	if _, err := os.Stat(defaultShell()); err != nil {
		t.Errorf("defaultShell() %q not found: %v", defaultShell(), err)
	}
}
