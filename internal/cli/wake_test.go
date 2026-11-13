package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/brettmostert/agent-command/internal/wake"
)

// envWaker, when a test sets it before env, is the test daemon's Waker.
var envWaker wake.Waker

// fakeMac plays sudo and pmset for both the CLI and its test daemon: the
// rule appears when the root script that adds it runs.
type fakeMac struct {
	mu      sync.Mutex
	rule    bool
	sched   string
	calls   []string
	scripts []string
	cancel  bool // the admin dialog is canceled
}

func (f *fakeMac) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	switch {
	case strings.HasPrefix(call, "/usr/bin/sudo -n -l "), strings.HasPrefix(call, "/usr/bin/sudo -n /usr/bin/pmset"):
		if !f.rule {
			return []byte("sudo: a password is required"), errors.New("exit status 1")
		}
	case call == "/usr/bin/pmset -g sched":
		return []byte(f.sched), nil
	}
	return nil, nil
}

func (f *fakeMac) asRoot(_ context.Context, _ io.Reader, _ io.Writer, script, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cancel {
		return errors.New("canceled")
	}
	f.scripts = append(f.scripts, script)
	switch {
	case strings.Contains(script, "/bin/mv -f"):
		f.rule = true
	case strings.Contains(script, "/bin/rm -f "+wake.RulePath):
		f.rule, f.sched = false, ""
	}
	return nil
}

func (f *fakeMac) hasRule() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rule
}

func (f *fakeMac) ran() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.scripts...)
}

// useFakeMac points the CLI and the next env's daemon at f.
func useFakeMac(t *testing.T, f *fakeMac) {
	t.Helper()
	mac := &wake.Mac{Run: f.run, Start: func(context.Context, string, ...string) (func(), error) { return func() {}, nil }}
	oldWaker, oldRoot, oldRule, oldEnv := localWaker, asRoot, ruleInstalled, envWaker
	localWaker, asRoot, ruleInstalled, envWaker = func() wake.Waker { return mac }, f.asRoot, f.hasRule, mac
	t.Cleanup(func() { localWaker, asRoot, ruleInstalled, envWaker = oldWaker, oldRoot, oldRule, oldEnv })
}

func TestWakeOnAndOff(t *testing.T) {
	f := &fakeMac{}
	useFakeMac(t, f)
	env(t)
	t.Chdir(t.TempDir())

	if out := mustRun(t, "schedule", "wake", "status"); !strings.Contains(out, "Waking the Mac is off") {
		t.Errorf("off: %q", out)
	}
	out := mustRun(t, "schedule", "add", "Brief", "--cron", "0 8 * * *", "--prompt", "p", "--wake")
	if !strings.Contains(out, "can't wake the Mac yet") {
		t.Errorf("hint: %q", out)
	}
	if out := mustRun(t, "schedule", "show", "Brief"); !strings.Contains(out, "Wakes the Mac") {
		t.Errorf("show: %q", out)
	}

	out = mustRun(t, "schedule", "wake", "on")
	for _, want := range []string{"Waking the Mac is on.", `Jobs that wake it: "Brief".`, "Next wake: ", "lid closed"} {
		if !strings.Contains(out, want) {
			t.Errorf("on is missing %q:\n%s", want, out)
		}
	}
	if s := f.ran(); len(s) != 1 || !strings.Contains(s[0], "NOPASSWD: /usr/bin/pmset ^schedule") {
		t.Errorf("install: %v", s)
	}
	if !strings.Contains(strings.Join(f.calls, "\n"), "/usr/bin/sudo -n /usr/bin/pmset schedule wake ") {
		t.Errorf("no wake set:\n%s", strings.Join(f.calls, "\n"))
	}

	// On again doesn't ask again; --all wakes for every job, and is kept.
	out = mustRun(t, "schedule", "wake", "on", "--all")
	if !strings.Contains(out, "Every job wakes it.") || len(f.ran()) != 1 {
		t.Errorf("all: %q %d", out, len(f.ran()))
	}
	if out := mustRun(t, "schedule", "wake", "on"); !strings.Contains(out, "Every job wakes it.") {
		t.Errorf("all kept: %q", out)
	}
	mustRun(t, "schedule", "wake", "on", "--all=false")
	mustRun(t, "schedule", "edit", "Brief", "--wake=false")
	if out := mustRun(t, "schedule", "wake", "status"); !strings.Contains(out, "No job wakes it yet") {
		t.Errorf("none: %q", out)
	}

	// Off cancels acmd's wakes and removes the rule, as root, in one go.
	f.mu.Lock()
	f.sched = " [0]  wake at 11/14/2026 07:58:00 by 'agent-command'\n"
	f.mu.Unlock()
	if out := mustRun(t, "schedule", "wake", "off"); !strings.Contains(out, "Waking the Mac is off.") {
		t.Errorf("off: %q", out)
	}
	s := f.ran()
	if last := s[len(s)-1]; !strings.Contains(last, "/usr/bin/pmset schedule cancel wake '11/14/26 07:58:00' agent-command || true") ||
		!strings.Contains(last, "/bin/rm -f "+wake.RulePath) {
		t.Errorf("off script: %q", last)
	}
	if out := mustRun(t, "schedule", "wake", "status"); !strings.Contains(out, "Waking the Mac is off") {
		t.Errorf("after off: %q", out)
	}
	// Off with nothing there asks for nothing.
	n := len(f.ran())
	mustRun(t, "schedule", "wake", "off")
	if len(f.ran()) != n {
		t.Error("off asked for a password with nothing to remove")
	}
}

func TestWakeOnFailures(t *testing.T) {
	f := &fakeMac{cancel: true}
	useFakeMac(t, f)
	env(t)
	if _, err := acmd(t, "schedule", "wake", "on"); err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Errorf("canceled: %v", err)
	}
	// The script ran but sudo still won't: say so.
	f.cancel = false
	asRoot = func(context.Context, io.Reader, io.Writer, string, string) error { return nil }
	if _, err := acmd(t, "schedule", "wake", "on"); err == nil || !strings.Contains(err.Error(), "still asks for a password") {
		t.Errorf("no rule after: %v", err)
	}
	f.rule = true
	asRoot = func(context.Context, io.Reader, io.Writer, string, string) error { return errors.New("no") }
	if _, err := acmd(t, "schedule", "wake", "off"); err == nil {
		t.Error("off failed silently")
	}
}

func TestWakeStatusWithoutAWaker(t *testing.T) {
	env(t)
	if out := mustRun(t, "schedule", "wake", "status"); !strings.Contains(out, "can't wake the Mac") {
		t.Errorf("%q", out)
	}
}

func TestUninstallRemovesTheWakeRule(t *testing.T) {
	f := &fakeMac{rule: true}
	useFakeMac(t, f)
	var b strings.Builder
	// What uninstall says and does for the rule, without the rest of it.
	if !ruleInstalled() {
		t.Fatal("no rule")
	}
	if err := removeWakeRule(context.Background(), strings.NewReader(""), &b); err != nil {
		t.Fatal(err)
	}
	if f.hasRule() {
		t.Error("rule kept")
	}
}

func TestInitOffersToWakeTheMac(t *testing.T) {
	f := &fakeMac{}
	useFakeMac(t, f)
	env(t)
	old := interactive
	t.Cleanup(func() { interactive = old })
	ctx := context.Background()

	// Not a terminal (Homebrew's install, the app): no question.
	interactive = func(io.Reader) bool { return false }
	var out strings.Builder
	if err := offerWake(ctx, strings.NewReader("y\n"), &out); err != nil || out.Len() != 0 || f.hasRule() {
		t.Errorf("asked without a terminal: %v %q", err, out.String())
	}

	interactive = func(io.Reader) bool { return true }
	out.Reset()
	if err := offerWake(ctx, strings.NewReader("\n"), &out); err != nil || !strings.Contains(out.String(), "Not now") || f.hasRule() {
		t.Errorf("no by default: %v %q", err, out.String())
	}
	out.Reset()
	if err := offerWake(ctx, strings.NewReader("y\n"), &out); err != nil || !f.hasRule() || !strings.Contains(out.String(), "Every job wakes it.") {
		t.Errorf("yes: %v %q", err, out.String())
	}
	// Already on: not asked again.
	out.Reset()
	if err := offerWake(ctx, strings.NewReader("y\n"), &out); err != nil || out.Len() != 0 {
		t.Errorf("asked again: %q", out.String())
	}

	// A canceled password dialog is said, and init still succeeds.
	f2 := &fakeMac{cancel: true}
	useFakeMac(t, f2)
	out.Reset()
	if err := offerWake(ctx, strings.NewReader("yes\n"), &out); err != nil || !strings.Contains(out.String(), "Couldn't turn on waking the Mac: adding the rule: canceled") {
		t.Errorf("canceled: %v %q", err, out.String())
	}
}
