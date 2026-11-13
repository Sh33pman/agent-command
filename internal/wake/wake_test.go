package wake

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeRun plays sudo and pmset, recording each call; nothing reaches the
// real power schedule.
type fakeRun struct {
	calls   []string
	sched   string
	allowed bool
	fail    string // a call that fails, by prefix
}

func (f *fakeRun) run(_ context.Context, name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if f.fail != "" && strings.HasPrefix(call, f.fail) {
		return []byte("sudo: a password is required"), errors.New("exit status 1")
	}
	switch {
	case strings.HasPrefix(call, sudo+" -n -l "):
		if !f.allowed {
			return nil, errors.New("exit status 1")
		}
		return []byte(pmset + "\n"), nil
	case call == pmset+" -g sched":
		return []byte(f.sched), nil
	}
	return nil, nil
}

const schedOut = `Scheduled power events:
 [0]  wake at 11/11/2026 21:59:43 by 'com.apple.alarm.user-invisible-com.apple.calaccessd.travelEngine.periodicRefreshTimer'
 [1]  wake at 11/14/2026 08:58:00 by 'agent-command'
 [2]  wake at 11/13/2026 07:00:00 by 'agent-command'
 [3]  wakepoweron at 11/15/2026 07:00:00 by 'agent-command'
 [4]  wake at 99/99/2026 07:00:00 by 'agent-command'
`

func TestOursReadsOnlyAcmdsWakes(t *testing.T) {
	got := ours([]byte(schedOut), time.UTC)
	want := []time.Time{time.Date(2026, 11, 13, 7, 0, 0, 0, time.UTC), time.Date(2026, 11, 14, 8, 58, 0, 0, time.UTC)}
	if !slices.EqualFunc(got, want, time.Time.Equal) {
		t.Errorf("ours: %v", got)
	}
	if got := ours([]byte("Scheduled power events:\n"), time.UTC); len(got) != 0 {
		t.Error(got)
	}
}

func TestMacCommands(t *testing.T) {
	f := &fakeRun{sched: schedOut}
	m := &Mac{Run: f.run}
	ctx := context.Background()
	if m.Allowed(ctx) {
		t.Error("allowed without the rule")
	}
	f.allowed = true
	if !m.Allowed(ctx) {
		t.Error("not allowed with the rule")
	}
	if _, err := m.Scheduled(ctx); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 14, 8, 58, 30, 0, time.Local)
	if err := m.Set(ctx, at); err != nil {
		t.Fatal(err)
	}
	if err := m.Cancel(ctx, at); err != nil {
		t.Fatal(err)
	}
	want := []string{
		sudo + " -n -l " + pmset + " schedule wake 01/01/30 00:00:00 agent-command",
		sudo + " -n -l " + pmset + " schedule wake 01/01/30 00:00:00 agent-command",
		pmset + " -g sched",
		sudo + " -n " + pmset + " schedule wake 11/14/26 08:58:30 agent-command",
		sudo + " -n " + pmset + " schedule cancel wake 11/14/26 08:58:30 agent-command",
	}
	if !slices.Equal(f.calls, want) {
		t.Errorf("calls:\n%s", strings.Join(f.calls, "\n"))
	}

	f.fail = sudo + " -n " + pmset
	if err := m.Set(ctx, at); err == nil || !strings.Contains(err.Error(), "password is required") {
		t.Errorf("a failed set: %v", err)
	}
	f.fail = pmset + " -g"
	if _, err := m.Scheduled(ctx); err == nil {
		t.Error("a failed read")
	}
}

func TestKeepAwake(t *testing.T) {
	var started []string
	stopped := false
	m := &Mac{Start: func(_ context.Context, name string, args ...string) (func(), error) {
		started = append(started, name+" "+strings.Join(args, " "))
		return func() { stopped = true }, nil
	}}
	stop, err := m.KeepAwake(context.Background(), 2*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	stop()
	if !slices.Equal(started, []string{"/usr/bin/caffeinate -i -t 7200"}) || !stopped {
		t.Errorf("%v %v", started, stopped)
	}
	if NewMac().Run == nil || NewMac().Start == nil {
		t.Error("the real Mac has no runner")
	}
}

// fakeWaker is a Waker in memory.
type fakeWaker struct {
	wakes  []time.Time
	log    []string
	failOn string
}

func (f *fakeWaker) Allowed(context.Context) bool { return true }
func (f *fakeWaker) Scheduled(context.Context) ([]time.Time, error) {
	if f.failOn == "read" {
		return nil, errors.New("no")
	}
	return slices.Clone(f.wakes), nil
}
func (f *fakeWaker) Set(_ context.Context, at time.Time) error {
	f.log = append(f.log, "set "+at.Format("15:04:05"))
	f.wakes = append(f.wakes, at)
	return nil
}
func (f *fakeWaker) Cancel(_ context.Context, at time.Time) error {
	f.log = append(f.log, "cancel "+at.Format("15:04:05"))
	if f.failOn == "cancel" {
		return errors.New("can't cancel")
	}
	f.wakes = slices.DeleteFunc(f.wakes, at.Equal)
	return nil
}
func (f *fakeWaker) KeepAwake(context.Context, time.Duration) (func(), error) { return func() {}, nil }

func TestSyncLeavesOneWake(t *testing.T) {
	ctx := context.Background()
	at := func(h, m int) time.Time { return time.Date(2026, 11, 14, h, m, 0, 0, time.UTC) }
	f := &fakeWaker{wakes: []time.Time{at(7, 0), at(8, 58)}}
	if err := Sync(ctx, f, at(8, 58).Add(400*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.log, []string{"cancel 07:00:00"}) {
		t.Errorf("keeps the one it wants: %v", f.log)
	}
	f.log = nil
	if err := Sync(ctx, f, at(9, 30)); err != nil || !slices.Equal(f.log, []string{"cancel 08:58:00", "set 09:30:00"}) {
		t.Errorf("moves it: %v %v", err, f.log)
	}
	f.log = nil
	if err := Sync(ctx, f, time.Time{}); err != nil || !slices.Equal(f.log, []string{"cancel 09:30:00"}) || len(f.wakes) != 0 {
		t.Errorf("clears it: %v %v", err, f.log)
	}
	f.failOn = "read"
	if err := Sync(ctx, f, at(9, 0)); err == nil {
		t.Error("a failed read")
	}
	f = &fakeWaker{wakes: []time.Time{at(7, 0)}, failOn: "cancel"}
	if err := Sync(ctx, f, at(9, 0)); err == nil || len(f.wakes) != 2 {
		t.Errorf("a failed cancel still sets the new one: %v %v", err, f.wakes)
	}
}

// ruleArgs is the rule's argument pattern, as sudo matches it: POSIX extended.
func ruleArgs(t *testing.T) *regexp.Regexp {
	t.Helper()
	rule, err := Rule("brett")
	if err != nil {
		t.Fatal(err)
	}
	_, args, ok := strings.Cut(strings.Split(rule, "\n")[2], pmset+" ")
	if !ok {
		t.Fatalf("no pmset in %q", rule)
	}
	return regexp.MustCompilePOSIX(args)
}

func TestTheRuleAllowsOnlyAcmdsWakes(t *testing.T) {
	re := ruleArgs(t)
	at := time.Date(2026, 11, 14, 8, 58, 30, 0, time.Local)
	for _, ok := range [][]string{setArgs(at), append([]string{"schedule", "cancel", "wake"}, setArgs(at)[2:]...), setArgs(probe)} {
		if s := strings.Join(ok, " "); !re.MatchString(s) {
			t.Errorf("doesn't allow %q", s)
		}
	}
	for _, bad := range []string{
		"schedule shutdown 11/14/26 08:58:30 agent-command",
		"schedule wake 11/14/26 08:58:30 someone-else",
		"schedule cancelall",
		"schedule wake 11/14/26 08:58:30 agent-command; rm -rf /",
		"schedule wake 11/14/26 08:58:30 agent-command sleep 11/14/26 09:00:00",
		"-a sleep 0",
		"sleepnow",
		"repeat wake MTWRFSU 08:00:00",
		"schedule cancel wake 11/14/26 08:58:30",
	} {
		if re.MatchString(bad) {
			t.Errorf("allows %q", bad)
		}
	}
}

func TestRuleAndScripts(t *testing.T) {
	rule, err := Rule("brett")
	if err != nil || !strings.HasPrefix(strings.Split(rule, "\n")[2], "brett ALL = (root) NOPASSWD: /usr/bin/pmset ^schedule") {
		t.Errorf("rule: %v\n%s", err, rule)
	}
	for _, bad := range []string{"", "root ALL=(ALL) ALL\nx", "a b", "1abc", "x;y", "#1"} {
		if _, err := Rule(bad); err == nil {
			t.Errorf("a rule for %q", bad)
		}
		if _, err := InstallScript(bad); err == nil {
			t.Errorf("a script for %q", bad)
		}
	}
	script, err := InstallScript("brett")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"umask 077", "/usr/bin/mktemp " + RulePath + ".XXXXXX", rule + "RULE\n",
		visudo + ` -cqf "$tmp"`, "chown root:wheel", "chmod 0440", `/bin/mv -f "$tmp" ` + RulePath} {
		if !strings.Contains(script, want) {
			t.Errorf("the script is missing %q:\n%s", want, script)
		}
	}
	if !strings.Contains(RemoveScript(), "rm -f "+RulePath) {
		t.Error(RemoveScript())
	}
	as := AppleScript(`echo "a\b"`, `Allow "it"`)
	if as != `do shell script "echo \"a\\b\"" with prompt "Allow \"it\"" with administrator privileges` {
		t.Error(as)
	}
}
