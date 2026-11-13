// Package wake wakes the Mac for scheduled jobs. Setting a wake needs root,
// so acmd installs a sudoers rule once (with an admin password) that lets the
// person run exactly two commands without one:
//
//	pmset schedule wake "MM/dd/yy HH:mm:ss" agent-command
//	pmset schedule cancel wake "MM/dd/yy HH:mm:ss" agent-command
//
// The owner, agent-command, marks the wakes acmd made, so it only ever
// changes its own.
package wake

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	// Owner marks the wakes acmd schedules.
	Owner = "agent-command"
	// RulePath is the sudoers rule that lets acmd set them.
	RulePath = "/etc/sudoers.d/agent-command"

	pmset   = "/usr/bin/pmset"
	sudo    = "/usr/bin/sudo"
	visudo  = "/usr/sbin/visudo"
	pmsetAt = "01/02/06 15:04:05" // what pmset schedule takes
)

// Waker sets and clears the Mac's scheduled wakes.
type Waker interface {
	// Allowed says whether the rule is in place, so wakes can be set without
	// a password.
	Allowed(ctx context.Context) bool
	// Scheduled is the wakes acmd has set, soonest first.
	Scheduled(ctx context.Context) ([]time.Time, error)
	Set(ctx context.Context, at time.Time) error
	Cancel(ctx context.Context, at time.Time) error
	// KeepAwake stops the Mac sleeping when idle until stop is called (or
	// for at most limit).
	KeepAwake(ctx context.Context, limit time.Duration) (stop func(), err error)
}

// Runner runs a command and returns its output. Swappable in tests, so they
// never touch the real power schedule.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Mac is the Waker for this Mac, through pmset.
type Mac struct {
	Run Runner
	// Start starts a long-running command (caffeinate); nil uses exec.
	Start func(ctx context.Context, name string, args ...string) (stop func(), err error)
}

// NewMac is the real Mac.
func NewMac() *Mac {
	return &Mac{
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).CombinedOutput() //nolint:gosec // G204: fixed programs, arguments acmd makes
		},
		Start: func(ctx context.Context, name string, args ...string) (func(), error) {
			cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // G204: caffeinate, with arguments acmd makes
			if err := cmd.Start(); err != nil {
				return nil, err
			}
			go func() { _ = cmd.Wait() }()
			return func() { _ = cmd.Process.Kill() }, nil
		},
	}
}

// probe is a wake time used only to ask sudo whether the rule allows it.
var probe = time.Date(2030, 1, 1, 0, 0, 0, 0, time.Local)

// Allowed asks sudo, without a password, whether a wake may be set.
func (m *Mac) Allowed(ctx context.Context) bool {
	_, err := m.Run(ctx, sudo, append([]string{"-n", "-l", pmset}, setArgs(probe)...)...)
	return err == nil
}

// Scheduled reads acmd's wakes from pmset -g sched, which needs no root.
func (m *Mac) Scheduled(ctx context.Context) ([]time.Time, error) {
	out, err := m.Run(ctx, pmset, "-g", "sched")
	if err != nil {
		return nil, fmt.Errorf("read the power schedule: %w", err)
	}
	return ours(out, time.Local), nil
}

func (m *Mac) Set(ctx context.Context, at time.Time) error {
	return m.sudo(ctx, "set a wake", setArgs(at))
}

func (m *Mac) Cancel(ctx context.Context, at time.Time) error {
	return m.sudo(ctx, "cancel a wake", append([]string{"schedule", "cancel", "wake"}, setArgs(at)[2:]...))
}

func (m *Mac) sudo(ctx context.Context, what string, args []string) error {
	out, err := m.Run(ctx, sudo, append([]string{"-n", pmset}, args...)...)
	if err != nil {
		return fmt.Errorf("%s: %w: %s", what, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// KeepAwake runs caffeinate -i, which holds off idle sleep, for at most limit.
func (m *Mac) KeepAwake(ctx context.Context, limit time.Duration) (func(), error) {
	return m.Start(ctx, "/usr/bin/caffeinate", "-i", "-t", fmt.Sprint(int(limit.Seconds())))
}

func setArgs(at time.Time) []string {
	return []string{"schedule", "wake", at.In(time.Local).Format(pmsetAt), Owner}
}

// sched is a line of pmset -g sched: " [0]  wake at 11/14/2026 08:58:00 by 'agent-command'".
var sched = regexp.MustCompile(`^\s*\[\d+\]\s+wake at (\d{2}/\d{2}/\d{4} \d{2}:\d{2}:\d{2}) by '([^']*)'`)

// ours is the wakes in pmset -g sched's output that acmd set, soonest first.
func ours(out []byte, loc *time.Location) []time.Time {
	var found []time.Time
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		m := sched.FindStringSubmatch(sc.Text())
		if m == nil || m[2] != Owner {
			continue
		}
		if t, err := time.ParseInLocation("01/02/2006 15:04:05", m[1], loc); err == nil {
			found = append(found, t)
		}
	}
	slices.SortFunc(found, func(a, b time.Time) int { return a.Compare(b) })
	return found
}

// Sync leaves acmd with exactly one wake, at want (to the second), or none
// when want is zero. It cancels any other wake acmd set.
func Sync(ctx context.Context, w Waker, want time.Time) error {
	have, err := w.Scheduled(ctx)
	if err != nil {
		return err
	}
	want = want.Truncate(time.Second)
	found := false
	var errs []error
	for _, t := range have {
		if !want.IsZero() && t.Equal(want) {
			found = true
			continue
		}
		errs = append(errs, w.Cancel(ctx, t))
	}
	if !want.IsZero() && !found {
		errs = append(errs, w.Set(ctx, want))
	}
	return errors.Join(errs...)
}

// userName is what a macOS short name can be, so it's safe in the rule.
var userName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

// Rule is the sudoers rule for user: the two pmset commands, with a date
// and acmd's owner, and nothing else.
func Rule(user string) (string, error) {
	if !userName.MatchString(user) {
		return "", fmt.Errorf("can't write a rule for the user %q", user)
	}
	return fmt.Sprintf(`# Agent Command: lets %[1]s set and cancel the Mac wakes acmd schedules for its
# jobs, without a password, and nothing else. "acmd schedule wake off" removes it.
%[1]s ALL = (root) NOPASSWD: %[2]s ^schedule (cancel )?wake [0-9]{2}/[0-9]{2}/[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2} %[3]s$
`, user, pmset, Owner), nil
}

// InstallScript is the shell script, run as root, that installs the rule:
// written in place with root's umask, checked with visudo, then moved in.
// Nothing the person can write is read as root.
func InstallScript(user string) (string, error) {
	rule, err := Rule(user)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`set -eu
umask 077
tmp=$(/usr/bin/mktemp %[1]s.XXXXXX)
trap '/bin/rm -f "$tmp"' EXIT
/bin/cat >"$tmp" <<'RULE'
%[2]sRULE
%[3]s -cqf "$tmp"
/usr/sbin/chown root:wheel "$tmp"
/bin/chmod 0440 "$tmp"
/bin/mv -f "$tmp" %[1]s
trap - EXIT
`, RulePath, rule, visudo), nil
}

// CancelScript is the shell lines, run as root, that cancel the given wakes
// of acmd's. A wake that has already gone is no error.
func CancelScript(wakes []time.Time) string {
	var b strings.Builder
	for _, t := range wakes {
		fmt.Fprintf(&b, "%s schedule cancel wake '%s' %s || true\n", pmset, t.In(time.Local).Format(pmsetAt), Owner)
	}
	return b.String()
}

// RemoveScript is the shell script, run as root, that removes the rule.
func RemoveScript() string {
	return "/bin/rm -f " + RulePath + "\n"
}

// AppleScript runs script as root through macOS's admin password dialog,
// for when there's no terminal to ask in.
func AppleScript(script, prompt string) string {
	quote := func(s string) string {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
	}
	return "do shell script " + quote(script) + " with prompt " + quote(prompt) + " with administrator privileges"
}
