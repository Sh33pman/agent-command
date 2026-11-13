package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/harness"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/schedule"
	"github.com/brettmostert/agent-command/internal/store"
)

// Scheduled jobs: the daemon checks them every ScheduleEvery and starts the
// ones that are due. A run is a hosted session like any other, so it shows in
// the app and lands in the Inbox when it needs you or finishes. Prompts are
// never logged.

// lateAfter is how late a run can start and still count as on time. Later than
// that, the Mac was asleep or acmd wasn't running: the run is missed, or runs
// late if the job says so.
const lateAfter = 2 * time.Minute

// maxPromptFile bounds a prompt file.
const maxPromptFile = 1 << 20

// nextRuns is how many upcoming runs a check shows.
const nextRuns = 5

// scheduler starts due jobs until the daemon stops. Runs are one at a time, so
// a slow start can't run a job twice.
func (d *Daemon) scheduler(ctx context.Context) {
	t := time.NewTicker(d.cfg.ScheduleEvery)
	defer t.Stop()
	defer d.releaseAwake(context.WithoutCancel(ctx), true)
	d.syncWake(ctx, true)
	for {
		if n := d.runDueJobs(ctx); n > 0 {
			d.changes.bump()
		}
		d.syncWake(ctx, false)
		d.releaseAwake(ctx, false)
		select {
		case <-ctx.Done():
			return
		case <-d.stop:
			return
		case <-t.C:
		}
	}
}

// runDueJobs deals with every job that's due, and returns how many it dealt with.
func (d *Daemon) runDueJobs(ctx context.Context) int {
	d.mu.Lock()
	jobs, err := d.store.Jobs(ctx)
	all, _ := d.store.AppState(ctx, keyWakeAll)
	d.mu.Unlock()
	if err != nil {
		d.cfg.Log.Printf("jobs: %v", err)
		return 0
	}
	now := d.cfg.Now()
	n := 0
	for _, j := range jobs {
		if j.Paused {
			continue
		}
		due, missed := d.dueRun(j, now)
		if due.IsZero() {
			continue
		}
		n++
		late := now.Sub(due) > lateAfter
		var run core.JobRun
		switch {
		case late && !j.RunMissed:
			run = core.JobRun{JobID: j.ID, Due: due, Result: core.RunMissed, Detail: missedDetail(missed)}
		default:
			run = d.runJob(ctx, j, due)
			if run.Result == core.RunStarted && wantsWake(j, all == "1") {
				d.keepAwake(ctx, run.SessionID)
			}
			if late {
				run.Detail = strings.TrimSpace(fmt.Sprintf("Ran late: it was due at %s. %s", due.In(d.cfg.Location).Format("Mon 2 Jan 15:04"), run.Detail))
			}
		}
		d.recordRun(ctx, j, &run)
	}
	return n
}

func missedDetail(n int) string {
	if n > 1 {
		return fmt.Sprintf("The Mac was asleep or acmd wasn't running (%d runs).", n)
	}
	return "The Mac was asleep or acmd wasn't running."
}

// dueRun is the latest run of j that's due by now (zero: none), and how many
// were due since the last one dealt with.
func (d *Daemon) dueRun(j *core.Job, now time.Time) (time.Time, int) {
	s := schedule.Schedule{Cron: j.Cron, Once: j.Once}
	var latest time.Time
	n := 0
	for t := j.LastDue; ; n++ {
		next := s.Next(t, d.cfg.Location)
		if next.IsZero() || next.After(now) || n > 100000 {
			break
		}
		latest, t = next, next
	}
	return latest, n
}

func (d *Daemon) recordRun(ctx context.Context, j *core.Job, run *core.JobRun) {
	d.mu.Lock()
	err := d.store.RecordRun(ctx, run)
	d.mu.Unlock()
	if err != nil {
		d.cfg.Log.Printf("job %s: recording its run: %v", j.ID, err)
		return
	}
	if run.Result == core.RunFailed {
		d.cfg.Log.Printf("job %s (%s): run failed: %s", j.ID, j.Name, run.Detail)
	}
}

// runJob starts one run of j, and says what happened. It doesn't record it.
func (d *Daemon) runJob(ctx context.Context, j *core.Job, due time.Time) core.JobRun {
	run := core.JobRun{JobID: j.ID, Due: due}
	fail := func(format string, a ...any) core.JobRun {
		run.Result, run.Detail = core.RunFailed, fmt.Sprintf(format, a...)
		return run
	}
	if d.cfg.Hosts == nil {
		return fail("Claude isn't available: run `acmd doctor`.")
	}
	if j.Harness != "" && j.Harness != core.HarnessClaude {
		return fail("acmd can't schedule %s yet.", j.Harness)
	}
	if j.Where != core.WhereSession && j.LastRun != nil && j.LastRun.Result == core.RunStarted && j.LastRun.SessionID != "" {
		d.mu.Lock()
		last, err := d.store.Session(ctx, j.LastRun.SessionID)
		d.mu.Unlock()
		if err == nil && busy(last.Status) {
			run.Result, run.Detail, run.SessionID = core.RunSkipped, "The last run was still going.", last.ID
			return run
		}
	}
	prompt, err := d.jobPrompt(j)
	if err != nil {
		return fail("%s", sentence(err))
	}
	args := harness.SettingsArgs(harness.SettingsFor(j.Harness), j.Settings)

	switch j.Where {
	case core.WhereFolder, core.WhereWorktree:
		if err := isDir(j.Cwd); err != nil {
			return fail("%s", sentence(err))
		}
		if j.Where == core.WhereWorktree {
			if err := inGitRepo(ctx, j.Cwd); err != nil {
				return fail("%s", sentence(err))
			}
			args = append(args, "--worktree", worktreeName(j, due.In(d.cfg.Location)))
		}
		s, err := d.hostSession(ctx, ipc.NewRequest{GroupID: d.jobGroup(ctx, j), Cwd: j.Cwd, Harness: core.HarnessClaude,
			Name: j.Name, Description: j.Description, Prompt: prompt, Args: args})
		if err != nil {
			return fail("Couldn't start the session: %s.", err)
		}
		run.Result, run.SessionID = core.RunStarted, s.ID
		return run
	case core.WhereSession:
		return d.runInSession(ctx, j, run, prompt, args)
	}
	return fail("The job doesn't say where to run.")
}

// runInSession sends the prompt to an existing session: it resumes a stopped
// one with it, types it into one that's finished its turn, and skips one
// that's busy.
func (d *Daemon) runInSession(ctx context.Context, j *core.Job, run core.JobRun, prompt string, args []string) core.JobRun {
	d.mu.Lock()
	s, err := d.store.Session(ctx, j.SessionID)
	d.mu.Unlock()
	run.SessionID = j.SessionID
	switch {
	case errors.Is(err, store.ErrNotFound):
		run.Result, run.Detail = core.RunFailed, "The session it continues has been deleted."
		return run
	case err != nil:
		run.Result, run.Detail = core.RunFailed, sentence(err)
		return run
	case s.Status == core.StatusArchived:
		run.Result, run.Detail = core.RunFailed, "The session it continues is archived."
		return run
	case s.Stopped():
		if _, err := d.resumeHosted(ctx, s, prompt, args); err != nil {
			run.Result, run.Detail = core.RunFailed, "Couldn't resume the session: "+sentence(err)
			return run
		}
		run.Result = core.RunStarted
		return run
	case s.Kind != core.KindHosted:
		run.Result, run.Detail = core.RunFailed, "The session runs outside acmd, so acmd can't send it the prompt."
		return run
	case s.Status != core.StatusDone && s.Status != core.StatusError:
		run.Result, run.Detail = core.RunSkipped, "The session was busy."
		return run
	}
	if len(args) > 0 {
		run.Detail = "The session was already running, so the job's settings didn't apply."
	}
	text := strings.ReplaceAll(prompt, "\x1b", "")
	for i, k := range [][]byte{[]byte("\x1b[200~" + text + "\x1b[201~"), []byte("\r")} {
		if i > 0 {
			time.Sleep(pasteEnterDelay)
		}
		if err := d.sendToHost(s.ID, k); err != nil {
			run.Result, run.Detail = core.RunFailed, "Couldn't reach the session: "+sentence(err)
			return run
		}
	}
	run.Result = core.RunStarted
	return run
}

func busy(st core.Status) bool {
	return st == core.StatusStarting || st == core.StatusBusy || st == core.StatusWaiting
}

// jobGroup is the job's group if it still exists, else the Default group.
func (d *Daemon) jobGroup(ctx context.Context, j *core.Job) string {
	if j.GroupID == "" {
		return ""
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if g, err := d.store.Group(ctx, j.GroupID); err == nil && g.ArchivedAt.IsZero() {
		return g.ID
	}
	return ""
}

// jobPrompt is the prompt for a run: the job's text, or its file read now.
func (d *Daemon) jobPrompt(j *core.Job) (string, error) {
	if j.PromptFile == "" {
		if strings.TrimSpace(j.Prompt) == "" {
			return "", errors.New("the job has no prompt")
		}
		return j.Prompt, nil
	}
	path := promptPath(j)
	st, err := os.Stat(path)
	switch {
	case err != nil:
		return "", fmt.Errorf("the prompt file %s isn't there", path)
	case st.IsDir():
		return "", fmt.Errorf("the prompt file %s is a folder", path)
	case st.Size() > maxPromptFile:
		return "", fmt.Errorf("the prompt file %s is over 1 MB", path)
	}
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("couldn't read the prompt file %s", path)
	}
	if strings.TrimSpace(string(b)) == "" {
		return "", fmt.Errorf("the prompt file %s is empty", path)
	}
	return string(b), nil
}

// promptPath is the prompt file's path, a relative one from the job's folder.
func promptPath(j *core.Job) string {
	p := expandHome(j.PromptFile)
	if !filepath.IsAbs(p) && j.Cwd != "" {
		p = filepath.Join(expandHome(j.Cwd), p)
	}
	return p
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

func isDir(p string) error {
	if p == "" {
		return errors.New("choose a folder")
	}
	if !filepath.IsAbs(p) {
		return fmt.Errorf("the folder %s isn't a full path", p)
	}
	st, err := os.Stat(p)
	if err != nil || !st.IsDir() {
		return fmt.Errorf("the folder %s isn't there", p)
	}
	return nil
}

func inGitRepo(ctx context.Context, dir string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--is-inside-work-tree") // #nosec G204 -- fixed git arguments
	c.Env = withoutGitVars(os.Environ())
	out, err := c.Output()
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		return fmt.Errorf("%s isn't in a git repository, so a worktree can't be made from it", dir)
	}
	return nil
}

// withoutGitVars drops GIT_DIR and the like, which would point git at another
// repo (as in a git hook) instead of the folder it's asked about.
func withoutGitVars(env []string) []string {
	var out []string
	for _, kv := range env {
		if !strings.HasPrefix(kv, "GIT_") {
			out = append(out, kv)
		}
	}
	return out
}

var unsafeName = regexp.MustCompile(`[^a-z0-9]+`)

// worktreeName names a run's worktree after the job and when it ran.
func worktreeName(j *core.Job, at time.Time) string {
	slug := strings.Trim(unsafeName.ReplaceAllString(strings.ToLower(j.Name), "-"), "-")
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
	}
	if slug == "" {
		slug = "job"
	}
	return slug + "-" + at.Format("20060102-1504")
}

// --- checking and saving ----------------------------------------------------

// checkJob reads j's schedule and checks everything, the same checks a save
// makes. With no errors, j is ready to save: its schedule is in canonical
// form, with Cron, Once and Words set.
func (d *Daemon) checkJob(ctx context.Context, j *core.Job) ipc.JobCheck {
	errs := map[string]string{}
	j.Name = strings.TrimSpace(j.Name)
	switch {
	case j.Name == "":
		errs["name"] = "Give the job a name."
	case len(j.Name) > 100:
		errs["name"] = "The name is over 100 characters."
	}
	if j.Harness != "" && j.Harness != core.HarnessClaude {
		errs["harness"] = "Only Claude Code can be scheduled for now."
	}

	var check ipc.JobCheck
	s, err := schedule.Parse(j.Schedule, d.cfg.Location, d.cfg.Now())
	if err != nil {
		errs["schedule"] = sentence(err)
	} else {
		j.Cron, j.Once = s.Cron, s.Once
		j.Words = s.Words(d.cfg.Location)
		j.Schedule = j.Words
		if j.Schedule == "" {
			j.Schedule = j.Cron
		}
		check.Cron, check.Words, check.Summary = j.Cron, j.Words, s.Summary(d.cfg.Location)
		check.Next = s.NextN(d.cfg.Now(), d.cfg.Location, nextRuns)
		if len(check.Next) == 0 {
			errs["schedule"] = "It never runs."
		}
	}

	if j.PromptFile != "" {
		if _, err := d.jobPrompt(j); err != nil {
			errs["prompt"] = sentence(err)
		}
	} else if strings.TrimSpace(j.Prompt) == "" {
		errs["prompt"] = "Write a prompt, or choose a Markdown file."
	}

	switch j.Where {
	case core.WhereFolder:
		if err := isDir(j.Cwd); err != nil {
			errs["cwd"] = sentence(err)
		}
		j.SessionID = ""
	case core.WhereWorktree:
		if err := isDir(j.Cwd); err != nil {
			errs["cwd"] = sentence(err)
		} else if err := inGitRepo(ctx, j.Cwd); err != nil {
			errs["cwd"] = sentence(err)
		}
		j.SessionID = ""
	case core.WhereSession:
		if msg := d.checkJobSession(ctx, j); msg != "" {
			errs["session"] = msg
		}
	default:
		errs["where"] = "Choose where it runs: a folder, a new worktree, or a session."
	}

	settings := harness.SettingsFor(j.Harness)
	if err := harness.CheckSettings(settings, j.Settings); err != nil {
		errs["settings"] = sentence(err)
	} else if msg := d.checkAgentFlags(settings, j.Settings); msg != "" {
		errs["settings"] = msg
	}
	j.Settings = tidySettings(j.Settings)

	if j.GroupID != "" {
		d.mu.Lock()
		g, err := d.store.Group(ctx, j.GroupID)
		d.mu.Unlock()
		if err != nil || !g.ArchivedAt.IsZero() {
			errs["group"] = "That group is gone."
		}
	}
	if len(errs) > 0 {
		check.Errors = errs
	}
	return check
}

// checkJobSession checks the session a job continues, and takes its folder.
func (d *Daemon) checkJobSession(ctx context.Context, j *core.Job) string {
	if j.SessionID == "" {
		return "Choose the session to continue."
	}
	d.mu.Lock()
	s, err := d.store.Session(ctx, j.SessionID)
	d.mu.Unlock()
	switch {
	case err != nil:
		return "That session is gone."
	case s.Status == core.StatusArchived:
		return "That session is archived."
	case s.ClaudeSessionID == "":
		return "That session has no Claude conversation to continue."
	case s.Kind == core.KindExternal:
		return "That session runs in your own terminal, outside acmd."
	}
	j.Cwd = s.Cwd
	return ""
}

// checkAgentFlags checks the installed agent takes the flags the settings
// need, so a typo or an old Claude Code fails now rather than at 08:00.
func (d *Daemon) checkAgentFlags(settings []harness.Setting, values map[string][]string) string {
	if d.cfg.AgentHelp == nil {
		return ""
	}
	help := d.cfg.AgentHelp()
	if help == "" {
		return ""
	}
	for _, s := range settings {
		if len(tidySettings(map[string][]string{"x": values[s.Key]})) == 0 {
			continue
		}
		if !strings.Contains(help, s.Flag) {
			return fmt.Sprintf("The installed Claude Code has no %s option: update it, or clear %s.", s.Flag, s.Label)
		}
	}
	return ""
}

// tidySettings drops empty values and settings.
func tidySettings(in map[string][]string) map[string][]string {
	out := map[string][]string{}
	for k, vs := range in {
		var keep []string
		for _, v := range vs {
			if v = strings.TrimSpace(v); v != "" {
				keep = append(keep, v)
			}
		}
		if len(keep) > 0 {
			out[k] = keep
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// sentence makes an error read as a sentence, for the app.
func sentence(err error) string {
	msg := capitalize(err.Error())
	if !strings.HasSuffix(msg, ".") {
		msg += "."
	}
	return msg
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// saveJob checks and saves a job: a new one if it has no ID.
func (d *Daemon) saveJob(ctx context.Context, j core.Job) (*core.Job, error) {
	check := d.checkJob(ctx, &j)
	if len(check.Errors) > 0 {
		return nil, errors.New(strings.TrimSuffix(firstError(check.Errors), "."))
	}
	saved := &core.Job{ID: j.ID, JobSpec: j.JobSpec, Cron: j.Cron, Once: j.Once, Words: j.Words}
	d.mu.Lock()
	defer d.mu.Unlock()
	if saved.ID != "" {
		old, err := d.store.Job(ctx, saved.ID)
		if err != nil {
			return nil, err
		}
		saved.Paused = old.Paused
	}
	if err := d.store.SaveJob(ctx, saved); err != nil {
		return nil, err
	}
	out, err := d.store.Job(ctx, saved.ID)
	if err != nil {
		return nil, err
	}
	d.withNextRun(out)
	return out, nil
}

// firstError is the errors in a fixed order, as one message.
func firstError(errs map[string]string) string {
	order := []string{"name", "harness", "schedule", "prompt", "where", "cwd", "session", "settings", "group"}
	keys := make([]string, 0, len(errs))
	for k := range errs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool { return slices.Index(order, keys[a]) < slices.Index(order, keys[b]) })
	msgs := make([]string, len(keys))
	for i, k := range keys {
		msgs[i] = errs[k]
	}
	return strings.Join(msgs, " ")
}

// withNextRun fills in when j runs next, and its words, which aren't stored.
func (d *Daemon) withNextRun(j *core.Job) {
	s := schedule.Schedule{Cron: j.Cron, Once: j.Once}
	j.Words = s.Words(d.cfg.Location)
	j.Summary = s.Summary(d.cfg.Location)
	j.NextRun = time.Time{}
	if !j.Paused {
		from := j.LastDue
		if now := d.cfg.Now(); now.After(from) {
			from = now
		}
		j.NextRun = s.Next(from, d.cfg.Location)
	}
}

// jobs lists the jobs for the snapshot (d.mu is held).
func (d *Daemon) jobs(ctx context.Context) ([]*core.Job, error) {
	jobs, err := d.store.Jobs(ctx)
	for _, j := range jobs {
		d.withNextRun(j)
	}
	return jobs, err
}

// runJobNow runs a job straight away, out of its schedule.
func (d *Daemon) runJobNow(ctx context.Context, id string) (core.JobRun, error) {
	d.mu.Lock()
	j, err := d.store.Job(ctx, id)
	d.mu.Unlock()
	if err != nil {
		return core.JobRun{}, err
	}
	now := d.cfg.Now()
	run := d.runJob(ctx, j, now)
	run.Due = now.UTC()
	if run.Detail == "" {
		run.Detail = "Run by hand."
	}
	d.recordRun(ctx, j, &run)
	if run.Result == core.RunFailed {
		return run, errors.New(strings.TrimSuffix(run.Detail, "."))
	}
	return run, nil
}

// handleJob carries out the job ops.
func (d *Daemon) handleJob(ctx context.Context, req ipc.Request) (any, error) {
	switch req.Op {
	case ipc.OpJobSave:
		j, err := decode[core.Job](req.Data)
		if err != nil {
			return nil, err
		}
		return d.saveJob(ctx, j)
	case ipc.OpJobCheck:
		j, err := decode[core.Job](req.Data)
		if err != nil {
			return nil, err
		}
		return d.checkJob(ctx, &j), nil
	case ipc.OpWakeSet:
		w, err := decode[ipc.WakeRef](req.Data)
		if err != nil {
			return nil, err
		}
		return d.setWake(ctx, w)
	}
	ref, err := decode[ipc.JobRef](req.Data)
	if err != nil {
		return nil, err
	}
	switch req.Op {
	case ipc.OpJobDelete:
		d.mu.Lock()
		defer d.mu.Unlock()
		return nil, d.store.DeleteJob(ctx, ref.ID)
	case ipc.OpJobPause:
		d.mu.Lock()
		defer d.mu.Unlock()
		return nil, d.store.SetJobPaused(ctx, ref.ID, ref.Paused)
	case ipc.OpJobRun:
		return d.runJobNow(ctx, ref.ID)
	case ipc.OpJobRuns:
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.store.JobRuns(ctx, ref.ID, 0)
	case ipc.OpAgentSettings:
		s := harness.SettingsFor(ref.Harness)
		if s == nil {
			return nil, fmt.Errorf("acmd can't schedule %q", ref.Harness)
		}
		return s, nil
	}
	return nil, fmt.Errorf("unknown op %q", req.Op)
}

// isJobOp says whether op is one of the job ops.
func isJobOp(op string) bool {
	switch op {
	case ipc.OpJobSave, ipc.OpJobCheck, ipc.OpJobDelete, ipc.OpJobPause, ipc.OpJobRun, ipc.OpJobRuns, ipc.OpAgentSettings, ipc.OpWakeSet:
		return true
	}
	return false
}
