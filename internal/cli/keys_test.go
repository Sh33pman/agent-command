package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/keymap"
	"github.com/brettmostert/agent-command/internal/kitty"
	"github.com/brettmostert/agent-command/internal/kitty/kittytest"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/setup"
)

func TestKeysCommand(t *testing.T) {
	e := viewEnvFor(t)
	p, _ := paths.Default()
	// init skipped a shortcut, and the user moved another.
	kittyDir := filepath.Join(e.dir, "kitty")
	must(t, os.MkdirAll(kittyDir, 0o700))
	must(t, os.WriteFile(filepath.Join(kittyDir, setup.KittyConfName), []byte("# skipped cmd+shift+a (next-attention): conflicts with Kitty's default select_all\n"), 0o600))
	must(t, setup.WriteRecord(p.Setup(), setup.Record{KittyDir: kittyDir}))
	cfg, _ := os.ReadFile(p.Config())
	must(t, os.WriteFile(p.Config(), append(cfg, []byte("[keys]\ntoggle = \"cmd+b\"\n")...), 0o600))
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("KITTY_WINDOW_ID", "")

	out := mustRun(t, "keys")
	for _, want := range []string{"Shortcuts · Kitty", "⌘B     hide/show sidebar", "next needing you (unbound: conflicts with Kitty's default select_all)", "── Sidebar ──"} {
		if !strings.Contains(out, want) {
			t.Errorf("keys lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "── Groups ──") {
		t.Error("group keys shown without groups")
	}
	mustRun(t, "group", "create", "PROJ")
	if out := mustRun(t, "keys", "--context", "groups", "--format", "markdown", "--terminal", "ghostty"); !strings.Contains(out, "## Groups") || strings.Contains(out, "## Sidebar") {
		t.Errorf("markdown groups:\n%s", out)
	}
	var bs []keymap.Binding
	must(t, json.Unmarshal([]byte(mustRun(t, "keys", "--format", "json", "--terminal", "ghostty")), &bs))
	if bs[0].Key != "⌘⇧N" { // the same combo as in Kitty
		t.Errorf("ghostty json: %+v", bs[0])
	}
	for _, bad := range [][]string{{"keys", "--terminal", "iterm"}, {"keys", "--context", "nope"}, {"keys", "--format", "yaml"}} {
		if _, err := acmd(t, bad...); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	t.Setenv("TERM_PROGRAM", "ghostty")
	if !strings.Contains(mustRun(t, "keys"), "Shortcuts · Ghostty") {
		t.Error("terminal not detected from TERM_PROGRAM")
	}
}

func TestOpenCommand(t *testing.T) {
	e := viewEnvFor(t)
	sim := kittytest.New()
	origKitty, origLaunch := kittyAt, editorLaunch
	kittyAt = func(string) *kitty.Kitty { return sim.Kitty() }
	var launched []string
	editorLaunch = func(_ context.Context, argv, _ []string, _ string) error {
		launched = append(launched, strings.Join(argv, " "))
		return nil
	}
	t.Cleanup(func() { kittyAt, editorLaunch = origKitty, origLaunch })
	p, _ := paths.Default()
	api := e.folder(t, "api", false)
	must(t, os.Mkdir(filepath.Join(e.dir, ".git"), 0o700)) // e.dir is the repo root
	mustRun(t, "new", "--cwd", api, "--name", "auth")

	// Nothing to open without a session or view; no editor yet without a terminal.
	if _, err := acmd(t, "open"); err == nil || !strings.Contains(err.Error(), "no session to open") {
		t.Errorf("open with nothing shown: %v", err)
	}
	if _, err := acmd(t, "open", "auth"); err == nil || !strings.Contains(err.Error(), "no editor chosen yet") {
		t.Errorf("unconfigured: %v", err)
	}
	// From a Kitty view: the editor picker opens in an overlay.
	var v core.View
	must(t, e.conn.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutViewOpen, Pane: &core.PaneLoc{Terminal: "kitty", KittySocket: "unix:/tmp/k", KittyWindow: 1}}, &v))
	mustRun(t, "open", "--kitty-window", "1")
	if last := sim.Calls[len(sim.Calls)-1]; !strings.HasPrefix(last, "launch --type=overlay --match id:1 -- ") || !strings.HasSuffix(last, " open "+e.snapshot(t).Sessions[0].ID) {
		t.Errorf("overlay: %s", last)
	}

	// Configured: the shown session's folder (or its git root) opens.
	code := filepath.Join(e.dir, "code")
	must(t, os.WriteFile(code, []byte("#!/bin/sh\n"), 0o755))
	must(t, setup.SaveEditor(p.Config(), config.Editor{Preset: "vscode", Resolved: code, RunIn: "background"}))
	mustRun(t, "open", "--kitty-window", "1")
	mustRun(t, "open", "auth", "--target", "git_root")
	realAPI, _ := filepath.EvalSymlinks(api)
	realRoot, _ := filepath.EvalSymlinks(e.dir)
	got := strings.Join(launched, ";")
	hasAPI := strings.Contains(got, realAPI) || strings.Contains(got, api)
	hasRoot := strings.Contains(got, code+" "+realRoot) || strings.Contains(got, code+" "+e.dir)
	if !strings.Contains(got, code+" ") || !hasAPI || !hasRoot {
		t.Errorf("launched: %s", got)
	}
	// --editor overrides once; a terminal editor opens in a Kitty tab.
	mustRun(t, "open", "auth", "--editor", "zed")
	if last := launched[len(launched)-1]; !strings.Contains(last, "zed") { // the launcher if installed, else open -b
		t.Errorf("--editor zed: %s", last)
	}
	must(t, setup.SaveEditor(p.Config(), config.Editor{Preset: "custom", Command: []string{code, "{dir}"}, Resolved: code, RunIn: "tab"}))
	mustRun(t, "open", "--kitty-window", "1")
	if last := sim.Calls[len(sim.Calls)-1]; !strings.HasPrefix(last, "launch --type=tab --match id:1 --cwd ") {
		t.Errorf("tab: %s", last)
	}
	editorLaunch = func(context.Context, []string, []string, string) error { return errors.New("boom") }
	must(t, setup.SaveEditor(p.Config(), config.Editor{Preset: "vscode", Resolved: code, RunIn: "background"}))
	if _, err := acmd(t, "open", "auth"); err == nil || err.Error() != "boom" {
		t.Errorf("launch error: %v", err)
	}
	if _, err := acmd(t, "editor", "pick"); err == nil || !strings.Contains(err.Error(), "needs a terminal") {
		t.Errorf("editor pick without a terminal: %v", err)
	}
}

func TestOpenSessionPicksEditorInteractively(t *testing.T) {
	e := viewEnvFor(t)
	origLaunch, origChoose := editorLaunch, chooseEditorTUI
	var launched string
	editorLaunch = func(_ context.Context, argv, _ []string, _ string) error {
		launched = strings.Join(argv, " ")
		return nil
	}
	chooseEditorTUI = func(context.Context, string) (*config.Editor, error) {
		return &config.Editor{Preset: "custom", Command: []string{"/bin/echo", "{dir}"}, Resolved: "/bin/echo", RunIn: "background"}, nil
	}
	t.Cleanup(func() { editorLaunch, chooseEditorTUI = origLaunch, origChoose })
	api := e.folder(t, "api", false)
	s := &core.Session{ID: "x", Cwd: api}
	must(t, openSession(context.Background(), s, nil, openOptions{interactive: true}))
	if launched != "/bin/echo "+api {
		t.Errorf("launched %q", launched)
	}
	p, _ := paths.Default()
	if cfg, _ := config.Load(p.Config()); cfg.Editor.Preset != "custom" {
		t.Errorf("choice not saved: %+v", cfg.Editor)
	}
	// Skipping the picker opens nothing.
	chooseEditorTUI = func(context.Context, string) (*config.Editor, error) { return nil, nil }
	must(t, setup.SaveEditor(p.Config(), config.Editor{}))
	if err := openSession(context.Background(), s, nil, openOptions{interactive: true}); err == nil {
		t.Error("skipped picker still opened")
	}
	if err := openSession(context.Background(), &core.Session{}, nil, openOptions{preset: "zed"}); err == nil {
		t.Error("session without a folder")
	}
}
