package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/harness"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/schedule"
)

// newScheduleCmd is `acmd schedule`: the scheduled jobs the app's Scheduled
// view shows, from the command line. A schedule is cron, or one time.
func newScheduleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "schedule",
		Aliases: []string{"jobs"},
		Short:   "Manage scheduled jobs: prompts Claude runs on a cron schedule",
		Long: `Manage scheduled jobs: prompts Claude runs on a cron schedule, or once.

A schedule is cron: five fields, minute hour day-of-month month day-of-week,
in this Mac's time zone. "0 9 * * 1-5" is weekdays at 09:00. Use --once for a
single run instead. A job runs only while the Mac is awake.`,
	}
	cmd.AddCommand(newScheduleListCmd(), newScheduleShowCmd(), newScheduleAddCmd(), newScheduleEditCmd(),
		newSchedulePauseCmd("pause", true), newSchedulePauseCmd("resume", false),
		newScheduleRunCmd(), newScheduleRunsCmd(), newScheduleDeleteCmd(),
		newScheduleExplainCmd(), newScheduleSettingsCmd(), newScheduleWakeCmd())
	return cmd
}

func newScheduleListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List scheduled jobs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withClient(cmd.Context(), func(_ *client, snap ipc.Snapshot) error {
				out := cmd.OutOrStdout()
				if len(snap.Jobs) == 0 {
					fmt.Fprintln(out, "No scheduled jobs. Add one with `acmd schedule add`.")
					return nil
				}
				tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "JOB\tSCHEDULE\tNEXT\tLAST\tSTATE")
				for _, j := range snap.Jobs {
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", j.Name, jobSchedule(j), whenText(j.NextRun), lastText(j.LastRun), jobState(j))
				}
				return tw.Flush()
			})
		},
	}
}

func newScheduleShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <job>",
		Short: "Show a job: its schedule, prompt, settings and latest runs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, snap ipc.Snapshot) error {
				j, err := resolveJob(snap, args[0])
				if err != nil {
					return err
				}
				var runs []core.JobRun
				if err := c.conn.Call(ipc.OpJobRuns, ipc.JobRef{ID: j.ID}, &runs); err != nil {
					return err
				}
				showJob(cmd.OutOrStdout(), j, snap, runs)
				return nil
			})
		},
	}
}

// jobFlags are the flags add and edit share.
type jobFlags struct {
	cron, once, prompt, promptFile, description string
	folder, worktree, session                   string
	set, unset                                  []string
	runMissed, wake                             bool
}

func (f *jobFlags) bind(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringVar(&f.cron, "cron", "", `when it runs, as cron: "minute hour day month weekday" (e.g. "0 9 * * 1-5")`)
	fl.StringVar(&f.once, "once", "", `run once instead, at a time like "2026-12-01 14:30"`)
	fl.StringVar(&f.prompt, "prompt", "", `the prompt (Markdown); "-" reads it from stdin`)
	fl.StringVar(&f.promptFile, "prompt-file", "", "a Markdown file read at each run instead of --prompt")
	fl.StringVar(&f.description, "description", "", "what the job is for")
	fl.StringVar(&f.folder, "folder", "", "run in a new session in this folder (default: the current folder)")
	fl.StringVar(&f.worktree, "worktree", "", "run in a new git worktree of this folder's repo")
	fl.StringVar(&f.session, "session", "", "continue this session's conversation (number, id or name)")
	fl.StringArrayVar(&f.set, "set", nil, "an agent setting, key=value (repeat for lists); see `acmd schedule settings`")
	fl.StringArrayVar(&f.unset, "unset", nil, "clear an agent setting, by key")
	fl.BoolVar(&f.runMissed, "run-missed", false, "run a run the Mac slept through when it wakes")
	fl.BoolVar(&f.wake, "wake", false, "wake the Mac shortly before each run (see `acmd schedule wake`)")
	cmd.MarkFlagsMutuallyExclusive("cron", "once")
	cmd.MarkFlagsMutuallyExclusive("prompt", "prompt-file")
	cmd.MarkFlagsMutuallyExclusive("folder", "worktree", "session")
}

// apply sets the flags that were given on j.
func (f *jobFlags) apply(cmd *cobra.Command, snap ipc.Snapshot, j *core.Job) error {
	changed := cmd.Flags().Changed
	switch {
	case changed("cron"):
		if _, err := schedule.ParseCron(f.cron); err != nil {
			return fmt.Errorf("--cron: %w", err)
		}
		j.Schedule = strings.Join(strings.Fields(f.cron), " ")
	case changed("once"):
		at, err := parseOnce(f.once)
		if err != nil {
			return err
		}
		j.Schedule = "once on " + at.Format("2006-01-02") + " at " + at.Format("15:04")
	}
	switch {
	case changed("prompt"):
		j.Prompt, j.PromptFile = f.prompt, ""
		if f.prompt == "-" {
			b, err := io.ReadAll(cmd.InOrStdin())
			if err != nil {
				return err
			}
			j.Prompt = string(b)
		}
	case changed("prompt-file"):
		p, err := absPath(f.promptFile)
		if err != nil {
			return err
		}
		j.PromptFile, j.Prompt = p, ""
	}
	if changed("description") {
		j.Description = f.description
	}
	if changed("run-missed") {
		j.RunMissed = f.runMissed
	}
	wakeFlag(cmd, f.wake, &j.Wake)
	switch {
	case changed("folder"), changed("worktree"):
		j.Where, j.SessionID = core.WhereFolder, ""
		dir := f.folder
		if changed("worktree") {
			j.Where, dir = core.WhereWorktree, f.worktree
		}
		p, err := absPath(dir)
		if err != nil {
			return err
		}
		j.Cwd = p
	case changed("session"):
		s, err := resolveSession(snap, f.session)
		if err != nil {
			return err
		}
		j.Where, j.SessionID, j.Cwd = core.WhereSession, s.ID, ""
	}
	return applySettings(j, f.set, f.unset)
}

// applySettings sets key=value settings, replacing what a key had, and
// clears the unset keys.
func applySettings(j *core.Job, set, unset []string) error {
	given := map[string][]string{}
	var order []string
	for _, kv := range set {
		k, v, ok := strings.Cut(kv, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return fmt.Errorf("--set %q: give it as key=value", kv)
		}
		if _, seen := given[k]; !seen {
			order = append(order, k)
		}
		given[k] = append(given[k], v)
	}
	if len(given) == 0 && len(unset) == 0 {
		return nil
	}
	if j.Settings == nil {
		j.Settings = map[string][]string{}
	}
	for _, k := range order {
		j.Settings[k] = given[k]
	}
	for _, k := range unset {
		delete(j.Settings, strings.TrimSpace(k))
	}
	return nil
}

func newScheduleAddCmd() *cobra.Command {
	var f jobFlags
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add a scheduled job",
		Example: `  acmd schedule add "Morning triage" --cron "0 9 * * 1-5" --prompt "Triage new issues"
  acmd schedule add "Release notes" --once "2026-12-01 14:30" --prompt-file notes.md --worktree .
  acmd schedule add Nightly --cron "30 2 * * *" --prompt - --set model=opus --set allowed-tools=Read < prompt.md`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("cron") && !cmd.Flags().Changed("once") {
				return errors.New("say when it runs: --cron, or --once")
			}
			if !cmd.Flags().Changed("prompt") && !cmd.Flags().Changed("prompt-file") {
				return errors.New("give it a prompt: --prompt, or --prompt-file")
			}
			return withClient(cmd.Context(), func(c *client, snap ipc.Snapshot) error {
				j := core.Job{JobSpec: core.JobSpec{Name: args[0], Where: core.WhereFolder}}
				if err := f.apply(cmd, snap, &j); err != nil {
					return err
				}
				if j.Where == core.WhereFolder && j.Cwd == "" {
					wd, err := os.Getwd()
					if err != nil {
						return err
					}
					j.Cwd = wd
				}
				return saveJob(cmd.OutOrStdout(), c, snap, j, "Added")
			})
		},
	}
	f.bind(cmd)
	return cmd
}

func newScheduleEditCmd() *cobra.Command {
	var f jobFlags
	var name string
	cmd := &cobra.Command{
		Use:   "edit <job>",
		Short: "Change a scheduled job: only the flags given change",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, snap ipc.Snapshot) error {
				found, err := resolveJob(snap, args[0])
				if err != nil {
					return err
				}
				j := *found
				j.Settings = maps(found.Settings)
				// The daemon keeps a schedule's words; send it back as cron or
				// a one-off, which is what this command takes.
				if j.Cron != "" {
					j.Schedule = j.Cron
				}
				if cmd.Flags().Changed("name") {
					j.Name = name
				}
				if err := f.apply(cmd, snap, &j); err != nil {
					return err
				}
				return saveJob(cmd.OutOrStdout(), c, snap, j, "Saved")
			})
		},
	}
	f.bind(cmd)
	cmd.Flags().StringVar(&name, "name", "", "rename the job")
	return cmd
}

func maps(in map[string][]string) map[string][]string {
	out := make(map[string][]string, len(in))
	for k, v := range in {
		out[k] = slices.Clone(v)
	}
	return out
}

// saveJob checks and saves j, and says when it runs next.
func saveJob(out io.Writer, c *client, snap ipc.Snapshot, j core.Job, verb string) error {
	var saved core.Job
	if err := c.conn.Call(ipc.OpJobSave, j, &saved); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s %q: %s.", verb, saved.Name, jobSchedule(&saved))
	if !saved.NextRun.IsZero() {
		fmt.Fprintf(out, " Next run %s.", whenText(saved.NextRun))
	}
	fmt.Fprintln(out)
	wakeHint(out, snap, saved.Wake)
	return nil
}

func newSchedulePauseCmd(use string, pause bool) *cobra.Command {
	short := "Pause a job, so it doesn't run until resumed"
	if !pause {
		short = "Resume a paused job"
	}
	return &cobra.Command{
		Use:   use + " <job>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, snap ipc.Snapshot) error {
				j, err := resolveJob(snap, args[0])
				if err != nil {
					return err
				}
				if err := c.conn.Call(ipc.OpJobPause, ipc.JobRef{ID: j.ID, Paused: pause}, nil); err != nil {
					return err
				}
				verb := "Paused"
				if !pause {
					verb = "Resumed"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s %q\n", verb, j.Name)
				return nil
			})
		},
	}
}

func newScheduleRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run <job>",
		Short: "Run a job now, outside its schedule",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, snap ipc.Snapshot) error {
				j, err := resolveJob(snap, args[0])
				if err != nil {
					return err
				}
				var run core.JobRun
				if err := c.conn.Call(ipc.OpJobRun, ipc.JobRef{ID: j.ID}, &run); err != nil {
					return err
				}
				out := cmd.OutOrStdout()
				switch run.Result {
				case core.RunSkipped:
					fmt.Fprintf(out, "Skipped %q: %s\n", j.Name, run.Detail)
				default:
					fmt.Fprintf(out, "Started %q%s\n", j.Name, sessionText(snap, run.SessionID, " in "))
				}
				return nil
			})
		},
	}
}

func newScheduleRunsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "runs <job>",
		Short: "List a job's runs, newest first",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, snap ipc.Snapshot) error {
				j, err := resolveJob(snap, args[0])
				if err != nil {
					return err
				}
				var runs []core.JobRun
				if err := c.conn.Call(ipc.OpJobRuns, ipc.JobRef{ID: j.ID}, &runs); err != nil {
					return err
				}
				writeRuns(cmd.OutOrStdout(), snap, runs)
				return nil
			})
		},
	}
}

func newScheduleDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <job>",
		Short: "Delete a job and its runs (its sessions stay)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, snap ipc.Snapshot) error {
				j, err := resolveJob(snap, args[0])
				if err != nil {
					return err
				}
				if err := confirm(cmd.InOrStdin(), cmd.OutOrStdout(), yes, fmt.Sprintf("Delete the job %q?", j.Name)); err != nil {
					return err
				}
				if err := c.conn.Call(ipc.OpJobDelete, ipc.JobRef{ID: j.ID}, nil); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Deleted %q\n", j.Name)
				return nil
			})
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask first")
	return cmd
}

func newScheduleExplainCmd() *cobra.Command {
	var count int
	cmd := &cobra.Command{
		Use:     "explain <cron>",
		Short:   "Say a cron schedule in English, and when it runs next",
		Example: `  acmd schedule explain "0 9 * * 1-5"`,
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Fields may come quoted as one argument or as five.
			c, err := schedule.ParseCron(strings.Join(args, " "))
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%s: %s\n", c.String(), schedule.Explain(c))
			for _, t := range (schedule.Schedule{Cron: c.String()}).NextN(time.Now(), time.Local, count) {
				fmt.Fprintf(out, "  %s\n", t.Format("Mon 2006-01-02 15:04"))
			}
			return nil
		},
	}
	cmd.Flags().IntVarP(&count, "next", "n", 5, "how many runs to list")
	return cmd
}

func newScheduleSettingsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "settings",
		Short: "List the agent settings a job can --set",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "KEY\tCLAUDE FLAG\tVALUES")
			for _, s := range harness.ClaudeSettings {
				values := s.Help
				if s.Kind == harness.KindChoice {
					values = strings.Join(s.Choices, ", ")
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\n", s.Key, s.Flag, values)
			}
			return tw.Flush()
		},
	}
}

// resolveJob finds a job by id, or by name: exact, then a unique prefix,
// ignoring case.
func resolveJob(snap ipc.Snapshot, sel string) (*core.Job, error) {
	sel = strings.TrimSpace(sel)
	for _, j := range snap.Jobs {
		if j.ID == sel {
			return j, nil
		}
	}
	for _, j := range snap.Jobs {
		if strings.EqualFold(j.Name, sel) {
			return j, nil
		}
	}
	var matches []*core.Job
	for _, j := range snap.Jobs {
		if sel != "" && strings.HasPrefix(strings.ToLower(j.Name), strings.ToLower(sel)) {
			matches = append(matches, j)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return nil, fmt.Errorf("no job named %q (see `acmd schedule list`)", sel)
	}
	names := make([]string, len(matches))
	for i, j := range matches {
		names[i] = fmt.Sprintf("%q", j.Name)
	}
	return nil, fmt.Errorf("%q could be %s", sel, strings.Join(names, " or "))
}

// parseOnce reads a one-off time, in this Mac's time zone.
func parseOnce(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02 at 15:04"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("--once %q: give the time as YYYY-MM-DD HH:MM", s)
}

func absPath(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return filepath.Abs(p)
}

// jobSchedule is a job's schedule to read: English, with the cron.
func jobSchedule(j *core.Job) string {
	switch {
	case j.Cron == "":
		return j.Summary
	case j.Summary == "" || j.Summary == j.Cron:
		return j.Cron
	}
	return j.Summary + " (" + j.Cron + ")"
}

func jobState(j *core.Job) string {
	switch {
	case j.Paused:
		return "paused"
	case j.NextRun.IsZero():
		return "done"
	}
	return ""
}

func whenText(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("Mon 2006-01-02 15:04")
}

func lastText(r *core.JobRun) string {
	if r == nil {
		return "-"
	}
	return r.Result + " " + r.At.Local().Format("01-02 15:04")
}

// sessionText names a session, after prefix ("" when there's none).
func sessionText(snap ipc.Snapshot, id, prefix string) string {
	if id == "" {
		return ""
	}
	for _, s := range snap.Sessions {
		if s.ID == id && s.Name != "" {
			return prefix + fmt.Sprintf("%q", s.Name)
		}
	}
	return prefix + id
}

func showJob(out io.Writer, j *core.Job, snap ipc.Snapshot, runs []core.JobRun) {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	row := func(k, v string) {
		if v != "" {
			fmt.Fprintf(tw, "%s\t%s\n", k, v)
		}
	}
	row("Job", j.Name)
	row("Description", j.Description)
	row("Schedule", jobSchedule(j))
	row("Next run", whenText(j.NextRun))
	row("State", jobState(j))
	switch j.Where {
	case core.WhereFolder:
		row("Runs in", "a new session in "+j.Cwd)
	case core.WhereWorktree:
		row("Runs in", "a new worktree of "+j.Cwd)
	case core.WhereSession:
		row("Runs in", "the session"+sessionText(snap, j.SessionID, " "))
	}
	if j.RunMissed {
		row("Missed runs", "run when the Mac wakes")
	}
	if j.Wake {
		row("Wakes the Mac", "yes, before each run")
	}
	keys := make([]string, 0, len(j.Settings))
	for k := range j.Settings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		row(k, strings.Join(j.Settings[k], ", "))
	}
	if j.PromptFile != "" {
		row("Prompt", "read from "+j.PromptFile)
	}
	_ = tw.Flush()
	if j.PromptFile == "" {
		fmt.Fprintf(out, "\n%s\n", strings.TrimRight(j.Prompt, "\n"))
	}
	if len(runs) > 0 {
		fmt.Fprintln(out)
		writeRuns(out, snap, runs[:min(len(runs), 5)])
	}
}

func writeRuns(out io.Writer, snap ipc.Snapshot, runs []core.JobRun) {
	if len(runs) == 0 {
		fmt.Fprintln(out, "No runs yet.")
		return
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "DUE\tRESULT\tSESSION\tDETAIL")
	for _, r := range runs {
		session := strings.Trim(sessionText(snap, r.SessionID, ""), `"`)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", whenText(r.Due), r.Result, session, r.Detail)
	}
	_ = tw.Flush()
}
