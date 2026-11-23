package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/daemon"
	"github.com/brettmostert/agent-command/internal/errcode"
	"github.com/brettmostert/agent-command/internal/harness"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/term"
)

func TestHostArgv(t *testing.T) {
	h := harness.Claude{Path: "/c", RemoteControl: true}
	if got, want := hostArgv(h, hostOptions{Dir: "/w", Name: "n", Prompt: "p", ClaudeSession: "u"}), []string{"/c", "--remote-control", "--name", "n", "--session-id", "u", "--", "p"}; !reflect.DeepEqual(got, want) {
		t.Errorf("new = %v", got)
	}
	if got, want := hostArgv(h, hostOptions{Resume: "sid", Name: "ignored"}), []string{"/c", "--remote-control", "--resume", "sid"}; !reflect.DeepEqual(got, want) {
		t.Errorf("resume = %v", got)
	}
	// A scheduled run continuing a session sends its prompt and settings.
	if got, want := hostArgv(h, hostOptions{Resume: "sid", Prompt: "# go", Args: []string{"--model", "opus"}}),
		[]string{"/c", "--remote-control", "--model", "opus", "--resume", "sid", "--", "# go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("resume with a prompt = %v", got)
	}
	// agy: the prompt joined to -i, on a new or a resumed conversation.
	a := harness.Antigravity{Path: "/agy"}
	if got, want := hostArgv(a, hostOptions{Dir: "/w", Name: "n", Prompt: "-p", ClaudeSession: "u", Args: []string{"--mode", "plan"}}),
		[]string{"/agy", "-i=-p", "--mode", "plan"}; !reflect.DeepEqual(got, want) {
		t.Errorf("agy new = %v", got)
	}
	if got, want := hostArgv(a, hostOptions{Resume: "conv", Prompt: "go"}), []string{"/agy", "--conversation", "conv", "-i=go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("agy resume = %v", got)
	}
	// A shell gets no prompt.
	if got := hostArgv(harness.Terminal{Shell: "/bin/zsh"}, hostOptions{Resume: "x", Prompt: "p"}); slices.Contains(got, "p") {
		t.Errorf("terminal = %v", got)
	}
}

func TestHostEnvStripsClaudeAndTerminal(t *testing.T) {
	env := hostEnv([]string{"PATH=/bin", "CLAUDECODE=1", "CLAUDE_CONFIG_DIR=/x", "TERM=dumb", "KITTY_WINDOW_ID=3", "HOME=/h"})
	if got, want := strings.Join(env, " "), "PATH=/bin HOME=/h TERM=xterm-256color"; got != want {
		t.Errorf("env = %q, want %q", got, want)
	}
}

// A host runs the harness argv under a PTY, serves it on HostSocket, and
// removes its socket and id file when the CLI exits. The "claude" here is a
// script that prints its argv and CLAUDE* presence, then waits for a line.
func TestHostServe(t *testing.T) {
	dir, err := os.MkdirTemp("", "acmdhost")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	p := paths.Paths{StateDir: filepath.Join(dir, "s"), RuntimeDir: filepath.Join(dir, "r"), ConfigDir: filepath.Join(dir, "c")}
	for k, v := range map[string]string{"ACMD_STATE_DIR": p.StateDir, "ACMD_RUNTIME_DIR": p.RuntimeDir, "ACMD_CONFIG_DIR": p.ConfigDir} {
		t.Setenv(k, v)
	}
	t.Setenv("CLAUDECODE", "1") // as if run from inside a Claude session
	for _, d := range []string{p.ConfigDir, p.RuntimeDir} {
		must(t, os.MkdirAll(d, 0o700))
	}
	script := filepath.Join(dir, "claude")
	must(t, os.WriteFile(script, []byte("#!/bin/sh\necho \"ARGS:$*\"\necho \"CLAUDECODE:${CLAUDECODE:-unset}\"\nread line\necho \"GOT:$line\"\n"), 0o700))
	// The old remote_control is still read by the host, as a session starts.
	must(t, os.WriteFile(p.Config(), []byte("remote_control = true\n"), 0o600))

	id := "01HZZZZZZZZZZZZZZZZZZZZZZZ"
	served := make(chan error, 1)
	go func() {
		_, err := acmd(t, "host", "serve", "--session", id, "--dir", dir, "--command", script, "--name", "auth", "--prompt", "hi", "--", "--model", "opus")
		served <- err
	}()
	sock := p.HostSocket(id)
	waitUntil(t, "host socket", func() bool { return term.Running(sock) })
	if _, err := os.Stat(p.HostIDFile(id)); err != nil {
		t.Errorf("id file: %v", err)
	}

	out := &syncBuf{}
	in := make(chan []byte, 1)
	size := make(chan [2]int)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	attached := make(chan error, 1)
	go func() { attached <- term.Attach(ctx, sock, in, size, 24, 80, out) }()
	waitUntil(t, "argv printed", func() bool { return strings.Contains(out.String(), "CLAUDECODE:unset") })
	if got := out.String(); !strings.Contains(got, "ARGS:--remote-control --name auth --model opus -- hi") {
		t.Errorf("argv not as expected:\n%s", got)
	}
	in <- []byte("go\n")
	waitUntil(t, "input reached the CLI", func() bool { return strings.Contains(out.String(), "GOT:go") })
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("host did not end when the CLI exited")
	}
	if term.Running(sock) {
		t.Error("socket still answers")
	}
	if _, err := os.Stat(p.HostIDFile(id)); !os.IsNotExist(err) {
		t.Errorf("id file left behind: %v", err)
	}
}

func TestHostServeNeedsSessionAndDir(t *testing.T) {
	env(t)
	if _, err := acmd(t, "host", "serve"); err == nil {
		t.Error("expected an error without --session/--dir")
	}
}

type syncBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestAttachLeavesQuietlyWithoutAHost(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "none.sock")
	start := time.Now()
	if err := attachOrLeave(context.Background(), sock, term.ModeStream, 150*time.Millisecond); err != nil {
		t.Errorf("attach with no host: %v", err)
	}
	if time.Since(start) < 150*time.Millisecond {
		t.Error("it didn't wait for a host that may be starting")
	}
}

func TestAttachHost(t *testing.T) {
	dir, err := os.MkdirTemp("", "acmdatt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "h.sock")
	if err := attachHost(context.Background(), sock, term.ModeStream, 0); err == nil {
		t.Error("attach without a host should fail")
	}
	// The CLI exits while attached: attach returns without an error.
	srv := &term.Server{Socket: sock, Shell: []string{"/bin/sh", "-c", "sleep 0.5"}, Dir: dir, Env: []string{"PATH=/usr/bin:/bin"}}
	go func() { _ = srv.Serve(context.Background()) }()
	waitUntil(t, "host socket", func() bool { return term.Running(sock) })
	// A mode the terminal doesn't have: not_supported, exit 11.
	err = attachHost(context.Background(), sock, term.ModeRendered, 0)
	if ExitCode(err) != exitNotSupported || errorCode(err) != errcode.NotSupported {
		t.Errorf("rendered: %v (exit %d, code %s)", err, ExitCode(err), errorCode(err))
	}
	done := make(chan error, 1)
	go func() { done <- attachHost(context.Background(), sock, term.ModeStream, 0) }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("attach after exit: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("attach did not return when the CLI exited")
	}
}

// An env.json saved before a restart has a dead SSH agent socket. Sessions get
// this login's one instead.
func TestLoginEnvUsesThisLoginsSSHAgent(t *testing.T) {
	p := paths.Paths{StateDir: t.TempDir()}
	saved := `{"PATH": "/usr/bin", "SSH_AUTH_SOCK": "/var/run/old/Listeners", "SECURITYSESSIONID": "1"}`
	if err := os.WriteFile(p.Env(), []byte(saved), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSH_AUTH_SOCK", "/var/run/new/Listeners")
	t.Setenv("SECURITYSESSIONID", "")
	os.Unsetenv("SECURITYSESSIONID")
	env := strings.Join(loginEnv(p), "\n")
	if !strings.Contains(env, "SSH_AUTH_SOCK=/var/run/new/Listeners") || strings.Contains(env, "/var/run/old") {
		t.Errorf("want this login's SSH agent:\n%s", env)
	}
	if strings.Contains(env, "SECURITYSESSIONID") {
		t.Errorf("a saved login-only variable the process doesn't have should go:\n%s", env)
	}
	if !strings.Contains(env, "PATH=/usr/bin") {
		t.Errorf("the saved PATH should stay:\n%s", env)
	}
}

// agy is found on the login shell's PATH; without it, starting a session says so.
func TestHostHarnessAntigravity(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{StateDir: filepath.Join(dir, "s"), RuntimeDir: filepath.Join(dir, "r"), ConfigDir: filepath.Join(dir, "c")}
	bin := filepath.Join(dir, "bin")
	must(t, os.MkdirAll(bin, 0o700))
	must(t, os.MkdirAll(p.StateDir, 0o700))
	t.Setenv("PATH", filepath.Join(dir, "nothing")) // not the real agy
	must(t, os.WriteFile(p.Env(), []byte(`{"PATH":"`+bin+`","HOME":"`+dir+`"}`), 0o600))

	if _, _, err := hostHarness(p, hostOptions{Harness: "antigravity"}); !errors.Is(err, errNoAgy) || err.Error() != "Antigravity's CLI (agy) isn't installed" {
		t.Errorf("without agy: %v", err)
	}
	if err := (execHosts{paths: p}).Start(context.Background(), daemon.HostRequest{ID: "x", Kind: "antigravity", Harness: &core.Harness{Kind: "antigravity"}, Dir: dir}); !errors.Is(err, errNoAgy) {
		t.Errorf("starting without agy: %v", err)
	}

	agy := filepath.Join(bin, "agy")
	must(t, os.WriteFile(agy, []byte("#!/bin/sh\n"), 0o700))
	h, env, err := hostHarness(p, hostOptions{Harness: "antigravity"})
	if err != nil {
		t.Fatal(err)
	}
	if a, ok := h.(harness.Antigravity); !ok || a.Path != agy || !slices.Contains(env, "PATH="+bin) {
		t.Errorf("harness %+v, env %v", h, env)
	}
}

// A harness runs its own program (or its kind's), and a job's settings are
// checked against each one's own --help (agy's is on stderr). A Claude
// harness with its own folder runs with CLAUDE_CONFIG_DIR.
func TestHarnessCommandAndHelp(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{StateDir: filepath.Join(dir, "s"), RuntimeDir: filepath.Join(dir, "r"), ConfigDir: filepath.Join(dir, "c")}
	for _, d := range []string{p.StateDir, p.ConfigDir} {
		must(t, os.MkdirAll(d, 0o700))
	}
	t.Setenv("PATH", filepath.Join(dir, "nothing"))
	t.Setenv("HOME", dir)
	must(t, os.WriteFile(p.Env(), []byte(`{"PATH":"`+filepath.Join(dir, "nothing")+`","CLAUDE_CONFIG_DIR":"/elsewhere"}`), 0o600))
	agy := filepath.Join(dir, "my-agy")
	claude := filepath.Join(dir, "my-claude")
	must(t, os.WriteFile(agy, []byte("#!/bin/sh\necho '  --mode   Set the agent execution mode' >&2\n"), 0o700))
	must(t, os.WriteFile(claude, []byte("#!/bin/sh\necho '  --permission-mode <mode>'\n"), 0o700))
	agyH := core.Harness{Kind: core.HarnessAntigravity, Command: agy}
	work := core.Harness{Kind: core.HarnessClaude, Command: claude, ConfigDir: "~/.claude-work"}
	std := core.Harness{Kind: core.HarnessClaude, Command: claude}

	if got, _, err := harnessCommand(p, agyH); err != nil || got != agy {
		t.Errorf("agy = %q, %v", got, err)
	}
	_, env, err := harnessCommand(p, work)
	must(t, err)
	if !slices.Contains(env, "CLAUDE_CONFIG_DIR="+filepath.Join(dir, ".claude-work")) || slices.Contains(env, "CLAUDE_CONFIG_DIR=/elsewhere") {
		t.Errorf("its own folder: %v", env)
	}
	if _, env, _ := harnessCommand(p, std); slices.ContainsFunc(env, func(kv string) bool {
		return strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") && kv != "CLAUDE_CONFIG_DIR=/elsewhere"
	}) {
		t.Errorf("Claude's standard folder sets nothing: %v", env)
	}
	help := harnessHelp(p)
	if got := help(agyH); !strings.Contains(got, "--mode") || strings.Contains(got, "--permission-mode") {
		t.Errorf("agy's help: %q", got)
	}
	if got := help(work); !strings.Contains(got, "--permission-mode") {
		t.Errorf("claude's help: %q", got)
	}

	missing := core.Harness{Kind: core.HarnessAntigravity, Command: "no-such-agy"}
	if _, _, err := harnessCommand(p, missing); !errors.Is(err, errNoAgy) {
		t.Errorf("a missing agy: %v", err)
	}
	if got := harnessHelp(p)(missing); got != "" {
		t.Errorf("help without agy: %q", got)
	}
	installed := installedHarnesses(p)
	if !installed(std) || installed(missing) || installed(core.Harness{Kind: core.HarnessClaude}) {
		t.Error("installed: a harness whose program is found")
	}
	r := daemon.HostRequest{ID: "x", Kind: core.HarnessClaude, Harness: &core.Harness{Kind: core.HarnessClaude, Command: "no-such-claude"}, Dir: dir}
	if err := (execHosts{paths: p}).Start(context.Background(), r); err == nil || !strings.Contains(err.Error(), "no-such-claude not found") {
		t.Errorf("starting without claude: %v", err)
	}
}

// --harness names a harness, ignoring case, or a kind, or terminal.
func TestPickHarness(t *testing.T) {
	hs := []harnessChoice{{"h1", "Claude", "claude"}, {"h2", "My Work", "claude"}, {"h3", "Antigravity", "antigravity"}}
	for sel, want := range map[string][2]string{
		"": {"", ""}, "claude": {"h1", "claude"}, "my work": {"h2", "claude"}, "MY_WORK": {"h2", "claude"},
		"h3": {"h3", "antigravity"}, "Terminal": {"", "terminal"},
	} {
		id, kind, err := pickHarness(hs, sel)
		if err != nil || id != want[0] || kind != want[1] {
			t.Errorf("%q: %q %q %v", sel, id, kind, err)
		}
	}
	if id, kind, err := pickHarness(hs[1:], "claude"); err != nil || id != "" || kind != "claude" {
		t.Errorf("a kind with no harness so named: %q %q %v", id, kind, err)
	}
	if _, _, err := pickHarness(hs, "copilot"); err == nil || !strings.Contains(err.Error(), `"My Work"`) {
		t.Errorf("unknown: %v", err)
	}
}
