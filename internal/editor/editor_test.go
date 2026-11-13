package editor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/config"
)

func opener(e config.Editor, onPath map[string]string, exists ...string) (*Opener, *[]string) {
	var launched []string
	ok := map[string]bool{}
	for _, p := range exists {
		ok[p] = true
	}
	return &Opener{
		Editor: e,
		Look: func(n string) (string, error) {
			if p, found := onPath[n]; found {
				return p, nil
			}
			return "", errors.New("not found")
		},
		Exists: func(p string) bool { return ok[p] },
		Launch: func(_ context.Context, argv, _ []string, _ string) error {
			launched = append(launched, strings.Join(argv, " "))
			if argv[0] == "/fail" || (len(argv) > 2 && argv[2] == "com.jetbrains.intellij") {
				return errors.New("boom")
			}
			return nil
		},
	}, &launched
}

func TestArgv(t *testing.T) {
	dir := "/w/my project"
	appOnlyWant, appOnlyErr := "", `couldn't open editor "zed": not found`
	if runtime.GOOS == "darwin" {
		appOnlyWant, appOnlyErr = "/usr/bin/open -b dev.zed.Zed "+dir, ""
	}
	cases := []struct {
		name   string
		e      config.Editor
		path   map[string]string
		exists []string
		want   string
		err    string
	}{
		{"resolved", config.Editor{Preset: "vscode", Resolved: "/opt/code"}, nil, []string{"/opt/code"}, "/opt/code " + dir, ""},
		{"moved launcher", config.Editor{Preset: "vscode", Resolved: "/gone/code"}, map[string]string{"code": "/new/code"}, nil, "/new/code " + dir, ""},
		{"app only", config.Editor{Preset: "zed"}, nil, nil, appOnlyWant, appOnlyErr},
		{"custom", config.Editor{Preset: "custom", Command: []string{"nvim", "-c", "cd {dir}", "{dir}"}}, map[string]string{"nvim": "/bin/nvim"}, nil,
			"/bin/nvim -c cd " + dir + " " + dir, ""},
		{"custom absolute", config.Editor{Preset: "custom", Command: []string{"/x/ed", "{dir}"}}, nil, []string{"/x/ed"}, "/x/ed " + dir, ""},
		{"custom missing", config.Editor{Preset: "custom", Command: []string{"nope", "{dir}"}}, nil, nil, "", `couldn't open editor "nope": not found`},
		{"custom empty", config.Editor{Preset: "custom"}, nil, nil, "", "has no command"},
		{"none", config.Editor{}, nil, nil, "", "no editor chosen"},
		{"unknown", config.Editor{Preset: "emacs"}, nil, nil, "", "unknown editor preset"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, _ := opener(c.e, c.path, c.exists...)
			argv, err := o.Argv(dir)
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Errorf("err %v, want %q", err, c.err)
				}
				return
			}
			if err != nil || strings.Join(argv, " ") != c.want {
				t.Errorf("argv %q %v, want %q", argv, err, c.want)
			}
		})
	}
	if !errors.Is(func() error { o, _ := opener(config.Editor{}, nil); _, err := o.Argv("/"); return err }(), ErrNotConfigured) {
		t.Error("ErrNotConfigured")
	}
}

func TestOpen(t *testing.T) {
	ctx := context.Background()
	if runtime.GOOS == "darwin" {
		o, launched := opener(config.Editor{Preset: "idea"}, nil)
		if err := o.Open(ctx, "/w"); err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(*launched, ";"); got != "/usr/bin/open -b com.jetbrains.intellij /w;/usr/bin/open -b com.jetbrains.intellij.ce /w" {
			t.Errorf("idea fallback to Community: %s", got)
		}
	}
	o, _ := opener(config.Editor{}, nil)
	if err := o.Open(ctx, "/w"); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("unconfigured: %v", err)
	}
}

func TestFolder(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := Folder(sub, "git_root"); got != root {
		t.Errorf("git root: %s", got)
	}
	if got := Folder(sub, "cwd"); got != sub {
		t.Errorf("cwd: %s", got)
	}
	outside := t.TempDir()
	if got := Folder(outside, "git_root"); got != outside {
		t.Errorf("outside a repo: %s", got)
	}
}

func TestStart(t *testing.T) {
	ctx := context.Background()
	if err := Start(ctx, []string{"/bin/sh", "-c", "exit 0"}, nil, "/"); err != nil {
		t.Errorf("quick success: %v", err)
	}
	err := Start(ctx, []string{"/bin/sh", "-c", "echo 'no such file' >&2; exit 3"}, nil, "/")
	if err == nil || !strings.Contains(err.Error(), `couldn't open editor "sh": no such file`) {
		t.Errorf("failure: %v", err)
	}
	if err := Start(ctx, []string{"/no/such/editor"}, nil, "/"); err == nil || !strings.Contains(err.Error(), "acmd editor set") {
		t.Errorf("missing binary: %v", err)
	}
	start := time.Now()
	if err := Start(ctx, []string{"/bin/sleep", "5"}, nil, "/"); err != nil || time.Since(start) > 4*time.Second {
		t.Errorf("long-running editor: %v after %v", err, time.Since(start))
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := Start(cctx, []string{"/bin/sleep", "1"}, nil, "/"); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled: %v", err)
	}
	var b strings.Builder
	l := &limited{b: &b, n: 3}
	_, _ = l.Write([]byte("abcdef"))
	if b.String() != "abc" {
		t.Errorf("limited: %q", b.String())
	}
}
