package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"strings"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/wake"
)

// wakeNote is what waking can't do, said wherever it's turned on.
const wakeNote = "A MacBook with its lid closed on battery may not wake, or may sleep again before the run: " +
	"on power, or with the lid open, it does."

// localWaker reads and checks this Mac's wakes; swappable in tests, so they
// never touch the real power schedule.
var localWaker = func() wake.Waker { return wake.NewMac() }

// asRoot runs a shell script as root: with sudo in a terminal it can ask on,
// else through macOS's admin password dialog. Swappable in tests.
var asRoot = func(ctx context.Context, in io.Reader, out io.Writer, script, prompt string) error {
	if f, ok := in.(*os.File); ok && isTerminal(f) && hasTTY() {
		fmt.Fprintln(out, prompt)
		cmd := exec.CommandContext(ctx, "/usr/bin/sudo", "/bin/sh", "-c", script) //nolint:gosec // G204: a script internal/wake writes, run where the person types their password
		cmd.Stdin, cmd.Stdout, cmd.Stderr = f, out, os.Stderr
		return cmd.Run()
	}
	b, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", wake.AppleScript(script, prompt)).CombinedOutput() //nolint:gosec // G204: as above, through the admin dialog
	if err != nil {
		if strings.Contains(string(b), "-128") {
			return errors.New("canceled")
		}
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(b)))
	}
	return nil
}

// ruleInstalled says whether acmd's sudoers rule is there (stat needs no root).
var ruleInstalled = func() bool {
	_, err := os.Stat(wake.RulePath)
	return err == nil
}

func newScheduleWakeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wake",
		Short: "Wake the Mac for scheduled jobs",
		Long: `Wake the Mac shortly before a job's run, so it runs on time.

"acmd schedule wake on" asks for your password once, to add a sudoers rule
(` + wake.RulePath + `) that lets you set and cancel acmd's own wakes, and
nothing else, without one. Then every job wakes the Mac with --all, or the jobs
added or edited with --wake. "acmd schedule wake off" clears acmd's wakes and
removes the rule.

` + wakeNote,
	}
	cmd.AddCommand(newWakeStatusCmd(), newWakeOnCmd(), newWakeOffCmd())
	return cmd
}

func newWakeStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Say whether acmd may wake the Mac, for which jobs, and when next",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withClient(cmd.Context(), func(_ *client, snap ipc.Snapshot) error {
				writeWake(cmd.OutOrStdout(), snap)
				return nil
			})
		},
	}
}

func writeWake(out io.Writer, snap ipc.Snapshot) {
	w := snap.Wake
	if w == nil {
		fmt.Fprintln(out, "This acmd can't wake the Mac.")
		return
	}
	if !w.Allowed {
		fmt.Fprintln(out, "Waking the Mac is off. Turn it on with `acmd schedule wake on`.")
		return
	}
	fmt.Fprintln(out, "Waking the Mac is on.")
	var names []string
	for _, j := range snap.Jobs {
		if wantsWake(j.Paused, j.Wake, w.All) {
			names = append(names, fmt.Sprintf("%q", j.Name))
		}
	}
	switch {
	case w.All:
		fmt.Fprintln(out, "Every job wakes it.")
	case len(names) == 0:
		fmt.Fprintln(out, "No job wakes it yet: add --wake to one, or turn it on for every job with `acmd schedule wake on --all`.")
	default:
		fmt.Fprintf(out, "Jobs that wake it: %s.\n", strings.Join(names, ", "))
	}
	if !w.Next.IsZero() {
		fmt.Fprintf(out, "Next wake: %s.\n", whenText(w.Next))
	}
	if w.Error != "" {
		fmt.Fprintf(out, "The last try failed: %s\n", w.Error)
	}
	fmt.Fprintln(out, wakeNote)
}

func wantsWake(paused, job, all bool) bool { return !paused && (all || job) }

func newWakeOnCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "on",
		Short: "Let acmd wake the Mac (asks for your password once)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var want *bool
			if cmd.Flags().Changed("all") {
				want = &all
			}
			return turnWakeOn(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), want)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "wake for every job, not only those with --wake (--all=false: only those)")
	return cmd
}

func newWakeOffCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "off",
		Short: "Clear acmd's wakes and remove its rule (asks for your password)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if err := removeWakeRule(ctx, cmd.InOrStdin(), cmd.OutOrStdout()); err != nil {
				return err
			}
			return withClient(ctx, func(c *client, _ ipc.Snapshot) error {
				if err := c.conn.Call(ipc.OpWakeSet, ipc.WakeRef{}, nil); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), "Waking the Mac is off.")
				return nil
			})
		},
	}
}

// removeWakeRule cancels acmd's wakes and removes its rule, as root, in one go
// (nothing to do if neither is there).
func removeWakeRule(ctx context.Context, in io.Reader, out io.Writer) error {
	wakes, err := localWaker().Scheduled(ctx)
	if err != nil {
		return err
	}
	if len(wakes) == 0 && !ruleInstalled() {
		return nil
	}
	script := wake.CancelScript(wakes) + wake.RemoveScript()
	prompt := "Agent Command wants to stop waking the Mac. This removes " + wake.RulePath + "."
	if err := asRoot(ctx, in, out, script, prompt); err != nil {
		return fmt.Errorf("removing the rule: %w", err)
	}
	return nil
}

// wakeFlag reads the --wake flag of add and edit.
func wakeFlag(cmd *cobra.Command, val bool, current *bool) {
	if cmd.Flags().Changed("wake") {
		*current = val
	}
}

// wakeHint says, after saving a job that wakes the Mac, if waking is off.
func wakeHint(out io.Writer, snap ipc.Snapshot, wakes bool) {
	if wakes && (snap.Wake == nil || !snap.Wake.Allowed) {
		fmt.Fprintln(out, "It can't wake the Mac yet: turn that on with `acmd schedule wake on`.")
	}
}

// addWakeRule installs the sudoers rule, as root, unless it's in place.
func addWakeRule(ctx context.Context, in io.Reader, out io.Writer) error {
	if localWaker().Allowed(ctx) {
		return nil
	}
	u, err := user.Current()
	if err != nil {
		return err
	}
	script, err := wake.InstallScript(u.Username)
	if err != nil {
		return err
	}
	prompt := "Agent Command wants to wake the Mac for scheduled jobs. This adds " + wake.RulePath + "."
	if err := asRoot(ctx, in, out, script, prompt); err != nil {
		return fmt.Errorf("adding the rule: %w", err)
	}
	if !localWaker().Allowed(ctx) {
		return errors.New("the rule is in place, but sudo still asks for a password: see `sudo -l`")
	}
	return nil
}

// turnWakeOn adds the rule, tells the daemon (all: wake for every job; nil
// keeps the setting), and says how it stands.
func turnWakeOn(ctx context.Context, in io.Reader, out io.Writer, all *bool) error {
	if err := addWakeRule(ctx, in, out); err != nil {
		return err
	}
	return withClient(ctx, func(c *client, snap ipc.Snapshot) error {
		var ref ipc.WakeRef
		switch {
		case all != nil:
			ref.All = *all
		case snap.Wake != nil:
			ref.All = snap.Wake.All
		}
		var st ipc.WakeState
		if err := c.conn.Call(ipc.OpWakeSet, ref, &st); err != nil {
			return err
		}
		snap.Wake = &st
		writeWake(out, snap)
		return nil
	})
}

// interactive says whether in is a terminal to ask in; swappable in tests.
var interactive = func(in io.Reader) bool {
	f, ok := in.(*os.File)
	return ok && isTerminal(f)
}

// offerWake is acmd init's question, in a terminal: let jobs wake the Mac?
// Anywhere else (Homebrew's install, the app) it says nothing; the app asks
// for itself.
func offerWake(ctx context.Context, in io.Reader, out io.Writer) error {
	if !interactive(in) || ruleInstalled() {
		return nil
	}
	fmt.Fprintf(out, "\nLet scheduled jobs wake the Mac shortly before they run? This adds a sudoers rule\n"+
		"(%s) that lets you set acmd's own wakes, and nothing else, without a password.\n"+
		"It asks for your password once. `acmd schedule wake off` removes it. [y/N] ", wake.RulePath)
	line, _ := bufio.NewReader(in).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
		fmt.Fprintln(out, "Not now: turn it on any time with `acmd schedule wake on`.")
		return nil
	}
	every := true
	if err := turnWakeOn(ctx, in, out, &every); err != nil {
		fmt.Fprintf(out, "Couldn't turn on waking the Mac: %v\n", err)
	}
	return nil
}

// hasTTY says whether this process has a terminal of its own, which sudo
// asks for the password on.
func hasTTY() bool {
	f, err := os.Open("/dev/tty")
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}
