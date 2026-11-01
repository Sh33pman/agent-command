package ghostty

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestScriptsCompile compiles every script against Ghostty's dictionary
// with osacompile (nothing runs). Opt-in: ACMD_IT_GHOSTTY=1 on a Mac with
// Ghostty installed.
func TestScriptsCompile(t *testing.T) {
	if os.Getenv("ACMD_IT_GHOSTTY") == "" {
		t.Skip("set ACMD_IT_GHOSTTY=1 to compile the AppleScripts against Ghostty")
	}
	dir := t.TempDir()
	n := 0
	rec := &App{Run: func(_ context.Context, script string, _ ...string) (string, error) {
		n++
		f := filepath.Join(dir, "s.applescript")
		if err := os.WriteFile(f, []byte(script), 0o600); err != nil {
			return "", err
		}
		if out, err := exec.Command("osacompile", "-o", filepath.Join(dir, "s.scpt"), f).CombinedOutput(); err != nil {
			t.Errorf("script %d doesn't compile: %s\n%s", n, out, script)
		}
		return "ok", nil
	}}
	ctx := context.Background()
	_, _ = rec.TermForTTY(ctx, "/dev/ttys001")
	_, _ = rec.AddSidebarLeft(ctx, "id", "cmd", "/", nil)
	_, _ = rec.Split(ctx, "id", "up", "cmd", "/", nil)
	_, _ = rec.Split(ctx, "id", "down", "cmd", "/", nil)
	_ = rec.Action(ctx, "id", "toggle_split_zoom")
	_ = rec.Focus(ctx, "id")
	_ = rec.Close(ctx, "id")
	_, _ = rec.Exists(ctx, "id")
	_ = rec.NewTab(ctx, "id", "cmd", "/", []string{"A=1"})
	_, _ = rec.Running(ctx)
	_ = rec.ReloadConfig(ctx)
	if n != 11 || strings.Contains(t.Name(), "x") {
		t.Errorf("compiled %d scripts", n)
	}
}
