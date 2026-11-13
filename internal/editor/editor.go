// Package editor opens a session's folder in the configured editor (SPEC
// §8.6): a preset's launcher (re-resolved if it moved, then `open -b
// <bundle id>`), or a custom argv with {dir}. Commands run directly,
// never through a shell, so folder names can't inject anything.
package editor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/setup"
)

// ErrNotConfigured means no editor was chosen yet.
var ErrNotConfigured = errors.New("no editor chosen yet")

// Launcher starts argv and reports a failure that happens within the
// grace period (production: Start).
type Launcher func(ctx context.Context, argv, env []string, dir string) error

// Opener opens folders.
type Opener struct {
	Editor config.Editor
	Env    []string                     // environment for the editor (login shell's)
	Look   func(string) (string, error) // finds an executable on the login PATH
	Exists func(string) bool            // an executable file exists
	Launch Launcher                     // background launch
}

// Folder is what to open for a session cwd: the cwd, or its git root when
// target is "git_root" (falling back to the cwd outside a repository).
func Folder(cwd, target string) string {
	if target != "git_root" {
		return cwd
	}
	for d := cwd; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		if d == filepath.Dir(d) {
			return cwd
		}
	}
}

// Argv returns the command that opens dir (for logs and tests).
func (o *Opener) Argv(dir string) ([]string, error) {
	e := o.Editor
	switch e.Preset {
	case "":
		return nil, ErrNotConfigured
	case "custom":
		if len(e.Command) == 0 {
			return nil, errors.New("the custom editor has no command (acmd editor set custom -- <command> {dir})")
		}
		argv := make([]string, len(e.Command))
		for i, a := range e.Command {
			argv[i] = strings.ReplaceAll(a, "{dir}", dir)
		}
		if p, err := o.Look(argv[0]); err == nil {
			argv[0] = p
		} else if !filepath.IsAbs(argv[0]) || !o.Exists(argv[0]) {
			return nil, fmt.Errorf("couldn't open editor %q: not found. Run: acmd editor set", e.Command[0])
		}
		return argv, nil
	}
	preset, ok := setup.PresetByName(e.Preset)
	if !ok {
		return nil, fmt.Errorf("unknown editor preset %q. Run: acmd editor set", e.Preset)
	}
	if e.Resolved != "" && o.Exists(e.Resolved) {
		return []string{e.Resolved, dir}, nil
	}
	if p, err := o.Look(preset.Launcher); err == nil {
		return []string{p, dir}, nil
	}
	if argv, ok := openBundle(preset, dir); ok {
		return argv, nil
	}
	return nil, fmt.Errorf("couldn't open editor %q: not found. Run: acmd editor set", preset.Launcher)
}

// Open opens dir.
func (o *Opener) Open(ctx context.Context, dir string) error {
	argv, err := o.Argv(dir)
	if err != nil {
		return err
	}
	err = o.Launch(ctx, argv, o.Env, dir)
	// `open -b` fails for an IDEA Community install under the Ultimate id.
	if err != nil && o.Editor.Preset == "idea" && len(argv) > 2 && argv[1] == "-b" {
		if preset, _ := setup.PresetByName("idea"); len(preset.Bundles) > 1 {
			err = o.Launch(ctx, []string{argv[0], "-b", preset.Bundles[1], dir}, o.Env, dir)
		}
	}
	return err
}

// Start launches argv detached (new session, no terminal) and waits up to
// 2 s: an exit with an error in that time is reported.
func Start(ctx context.Context, argv, env []string, dir string) error {
	cmd := exec.Command(argv[0], argv[1:]...) // #nosec G204 -- the user's configured editor, argv without a shell
	cmd.Dir, cmd.Env = dir, env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	var stderr strings.Builder
	cmd.Stderr = &limited{b: &stderr, n: 2000}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("couldn't open editor %q: %w. Run: acmd editor set", filepath.Base(argv[0]), err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = err.Error()
			}
			return fmt.Errorf("couldn't open editor %q: %s", filepath.Base(argv[0]), firstLine(msg))
		}
		return nil
	case <-time.After(2 * time.Second):
		return nil // still running (a GUI editor's window): fine; the goroutine reaps it
	case <-ctx.Done():
		return ctx.Err()
	}
}

type limited struct {
	b *strings.Builder
	n int
}

func (l *limited) Write(p []byte) (int, error) {
	if room := l.n - l.b.Len(); room > 0 {
		l.b.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
