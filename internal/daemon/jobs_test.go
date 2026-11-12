package daemon

import (
	"context"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/harness"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/store"
)

// clock is a test's time, which it moves on by hand.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) set(s string) {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.UTC)
	if err != nil {
		panic(err)
	}
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

// typedKeys records what was typed into sessions.
type typedKeys struct {
	mu   sync.Mutex
	keys map[string][]string
}

func (s *typedKeys) send(id string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.keys == nil {
		s.keys = map[string][]string{}
	}
	s.keys[id] = append(s.keys[id], string(data))
	return nil
}

func (s *typedKeys) to(id string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.keys[id]...)
}

// jobDaemon is a daemon that isn't listening, for driving the scheduler by hand
// on a test's clock. Hosts are fake.
func jobDaemon(t *testing.T) (*Daemon, *fakeHosts, *clock, *typedKeys) {
	t.Helper()
	p := testPaths(t)
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	clk := &clock{}
	clk.set("2026-11-12 07:00:00")
	st, err := store.Open(context.Background(), p.DB(), store.WithClock(clk.now))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fh := &fakeHosts{paths: p}
	keys := &typedKeys{}
	d := &Daemon{store: st, stop: make(chan struct{}), cfg: Config{
		Paths: p, Log: log.New(io.Discard, "", 0), Now: clk.now, Location: time.UTC,
		Hosts: fh, SendToHost: keys.send, ScheduleEvery: time.Hour,
	}}
	return d, fh, clk, keys
}

func folderJob(dir string) core.Job {
	return core.Job{JobSpec: core.JobSpec{Name: "Morning brief", Schedule: "every day at 08:00", Prompt: "# Brief\n\nSummarize my day.",
		Where: core.WhereFolder, Cwd: dir, Settings: map[string][]string{"model": {"opus"}, "effort": {""}}}}
}

func mustSave(t *testing.T, d *Daemon, j core.Job) *core.Job {
	t.Helper()
	saved, err := d.saveJob(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func lastRun(t *testing.T, d *Daemon, id string) core.JobRun {
	t.Helper()
	runs, err := d.store.JobRuns(context.Background(), id, 1)
	if err != nil || len(runs) == 0 {
		t.Fatalf("no runs (%v)", err)
	}
	return runs[0]
}

func TestCheckJob(t *testing.T) {
	d, _, _, _ := jobDaemon(t)
	ctx := context.Background()
	dir := t.TempDir()

	ok := folderJob(dir)
	ok.Schedule = "0 8 * * MON-FRI"
	check := d.checkJob(ctx, &ok)
	if len(check.Errors) != 0 {
		t.Fatalf("valid job: %v", check.Errors)
	}
	if check.Cron != "0 8 * * 1-5" || check.Words != "weekdays at 08:00" || ok.Schedule != "weekdays at 08:00" || len(check.Next) != nextRuns {
		t.Errorf("schedule: %+v (job's %q)", check, ok.Schedule)
	}
	if !check.Next[0].Equal(time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("next: %v", check.Next)
	}
	if _, has := ok.Settings["effort"]; has {
		t.Errorf("empty settings kept: %v", ok.Settings)
	}
	// Cron with no words keeps its cron.
	cron := folderJob(dir)
	cron.Schedule = "0,30 9 * * *"
	if check := d.checkJob(ctx, &cron); len(check.Errors) != 0 || cron.Schedule != "*/30 9 * * *" || check.Words != "" ||
		check.Summary != "at 09:00 and 09:30, every day" {
		t.Errorf("cron only: %+v %q", check, cron.Schedule)
	}

	file := filepath.Join(dir, "prompt.md")
	for name, tc := range map[string]struct {
		edit  func(*core.Job)
		field string
		want  string
	}{
		"no name":       {func(j *core.Job) { j.Name = "  " }, "name", "Give the job a name"},
		"long name":     {func(j *core.Job) { j.Name = strings.Repeat("x", 101) }, "name", "over 100"},
		"bad schedule":  {func(j *core.Job) { j.Schedule = "sometimes" }, "schedule", "Couldn't read"},
		"past":          {func(j *core.Job) { j.Schedule = "once on 2026-11-11 at 08:00" }, "schedule", "passed"},
		"no prompt":     {func(j *core.Job) { j.Prompt = " \n" }, "prompt", "Write a prompt"},
		"no file":       {func(j *core.Job) { j.PromptFile = file }, "prompt", "isn't there"},
		"no where":      {func(j *core.Job) { j.Where = "" }, "where", "Choose where"},
		"no folder":     {func(j *core.Job) { j.Cwd = "" }, "cwd", "Choose a folder"},
		"relative":      {func(j *core.Job) { j.Cwd = "notes" }, "cwd", "full path"},
		"gone":          {func(j *core.Job) { j.Cwd = filepath.Join(dir, "gone") }, "cwd", "isn't there"},
		"not a repo":    {func(j *core.Job) { j.Where = core.WhereWorktree }, "cwd", "git repository"},
		"no session":    {func(j *core.Job) { j.Where = core.WhereSession }, "session", "Choose the session"},
		"lost session":  {func(j *core.Job) { j.Where, j.SessionID = core.WhereSession, "nope" }, "session", "gone"},
		"bad setting":   {func(j *core.Job) { j.Settings = map[string][]string{"effort": {"huge"}} }, "settings", "one of low"},
		"other harness": {func(j *core.Job) { j.Harness = "codex" }, "harness", "Only Claude Code"},
		"lost group":    {func(j *core.Job) { j.GroupID = "nope" }, "group", "gone"},
	} {
		j := folderJob(dir)
		tc.edit(&j)
		check := d.checkJob(ctx, &j)
		if got := check.Errors[tc.field]; !strings.Contains(got, tc.want) {
			t.Errorf("%s: %s = %q, want %q (all: %v)", name, tc.field, got, tc.want, check.Errors)
		}
	}

	// A prompt file, relative to the folder.
	must(t, os.WriteFile(file, []byte("# From a file"), 0o600))
	withFile := folderJob(dir)
	withFile.Prompt, withFile.PromptFile = "", "prompt.md"
	if check := d.checkJob(ctx, &withFile); len(check.Errors) != 0 {
		t.Errorf("prompt file: %v", check.Errors)
	}
	for name, content := range map[string][]byte{"empty": []byte(" \n"), "big": make([]byte, maxPromptFile+1)} {
		must(t, os.WriteFile(file, content, 0o600))
		if check := d.checkJob(ctx, &withFile); !strings.Contains(check.Errors["prompt"], "is ") {
			t.Errorf("%s prompt file: %v", name, check.Errors)
		}
	}
	withFile.PromptFile = dir
	if check := d.checkJob(ctx, &withFile); !strings.Contains(check.Errors["prompt"], "folder") {
		t.Errorf("prompt file that's a folder: %v", check.Errors)
	}

	// Settings are checked against the installed Claude Code.
	d.cfg.AgentHelp = func() string { return "--model <model>\n--permission-mode" }
	needsEffort := folderJob(dir)
	needsEffort.Settings = map[string][]string{"effort": {"high"}}
	if check := d.checkJob(ctx, &needsEffort); !strings.Contains(check.Errors["settings"], "no --effort option") {
		t.Errorf("old claude: %v", check.Errors)
	}
	if check := d.checkJob(ctx, &ok); len(check.Errors) != 0 {
		t.Errorf("flags it has: %v", check.Errors)
	}
	d.cfg.AgentHelp = func() string { return "" } // couldn't run it: don't block
	if check := d.checkJob(ctx, &needsEffort); len(check.Errors) != 0 {
		t.Errorf("no help: %v", check.Errors)
	}

	// Saving refuses what the check refuses, with the errors in order.
	bad := folderJob(dir)
	bad.Name, bad.Prompt = "", ""
	if _, err := d.saveJob(ctx, bad); err == nil || err.Error() != "Give the job a name. Write a prompt, or choose a Markdown file" {
		t.Errorf("save: %v", err)
	}
}

func TestScheduledRuns(t *testing.T) {
	d, fh, clk, _ := jobDaemon(t)
	ctx := context.Background()
	dir := t.TempDir()
	g, err := d.store.CreateGroup(ctx, "Scheduled", "")
	must(t, err)
	j := folderJob(dir)
	j.GroupID, j.Description = g.ID, "every morning"
	job := mustSave(t, d, j)
	if !job.NextRun.Equal(time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)) || job.Words != "every day at 08:00" || job.Summary != job.Words {
		t.Errorf("saved: next %s, words %q", job.NextRun, job.Words)
	}

	clk.set("2026-11-12 07:59:00")
	if n := d.runDueJobs(ctx); n != 0 {
		t.Fatalf("ran early: %d", n)
	}
	clk.set("2026-11-12 08:00:20")
	if n := d.runDueJobs(ctx); n != 1 {
		t.Fatalf("due: %d", n)
	}
	run := lastRun(t, d, job.ID)
	if run.Result != core.RunStarted || run.SessionID == "" || !run.Due.Equal(time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("run: %+v", run)
	}
	reqs := fh.requests()
	if len(reqs) != 1 || reqs[0].Prompt != j.Prompt || reqs[0].Dir != dir || reqs[0].Name != "Morning brief" ||
		!slices.Equal(reqs[0].Args, []string{"--model", "opus"}) {
		t.Errorf("host: %+v", reqs)
	}
	s, err := d.store.Session(ctx, run.SessionID)
	must(t, err)
	if s.GroupID != g.ID || s.Description != "every morning" || s.Name != "Morning brief" {
		t.Errorf("session: %+v", s)
	}
	if n := d.runDueJobs(ctx); n != 0 {
		t.Errorf("ran twice: %d", n)
	}

	// The next day the last run is still going: skip.
	s.Status = core.StatusBusy
	must(t, d.store.UpdateSession(ctx, s))
	clk.set("2026-11-13 08:00:00")
	d.runDueJobs(ctx)
	if run := lastRun(t, d, job.ID); run.Result != core.RunSkipped || len(fh.requests()) != 1 {
		t.Errorf("still going: %+v", run)
	}
	s.Status = core.StatusDone
	must(t, d.store.UpdateSession(ctx, s))

	// The Mac slept through two days: one missed run, not two.
	clk.set("2026-11-15 10:00:00")
	d.runDueJobs(ctx)
	run = lastRun(t, d, job.ID)
	if run.Result != core.RunMissed || !strings.Contains(run.Detail, "2 runs") || !run.Due.Equal(time.Date(2026, 11, 15, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("missed: %+v", run)
	}
	if n := d.runDueJobs(ctx); n != 0 || len(fh.requests()) != 1 {
		t.Errorf("missed runs ran: %d", n)
	}

	// With RunMissed it runs late instead.
	edit, _ := d.store.Job(ctx, job.ID)
	edit.RunMissed = true
	mustSave(t, d, *edit)
	clk.set("2026-11-16 09:30:00")
	d.runDueJobs(ctx)
	if run := lastRun(t, d, job.ID); run.Result != core.RunStarted || !strings.Contains(run.Detail, "Ran late") {
		t.Errorf("run late: %+v", run)
	}

	// Paused jobs don't run, and resuming doesn't make up for the pause.
	must(t, d.store.SetJobPaused(ctx, job.ID, true))
	clk.set("2026-11-18 08:00:00")
	if n := d.runDueJobs(ctx); n != 0 {
		t.Errorf("paused job ran")
	}
	snap, _ := d.jobs(ctx)
	if !snap[0].NextRun.IsZero() {
		t.Errorf("paused job's next run: %s", snap[0].NextRun)
	}
	must(t, d.store.SetJobPaused(ctx, job.ID, false))
	if n := d.runDueJobs(ctx); n != 0 {
		t.Errorf("resumed job made up a run")
	}
	snap, _ = d.jobs(ctx)
	if !snap[0].NextRun.Equal(time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("resumed: next run %s", snap[0].NextRun)
	}
}

func TestRunFailures(t *testing.T) {
	d, _, clk, _ := jobDaemon(t)
	ctx := context.Background()
	dir := t.TempDir()
	file := filepath.Join(dir, "brief.md")
	must(t, os.WriteFile(file, []byte("# Brief"), 0o600))
	j := folderJob(dir)
	j.Prompt, j.PromptFile = "", file
	job := mustSave(t, d, j)

	// The file is read at the run, so a file gone since fails it, clearly.
	must(t, os.Remove(file))
	clk.set("2026-11-12 08:00:00")
	d.runDueJobs(ctx)
	if run := lastRun(t, d, job.ID); run.Result != core.RunFailed || !strings.Contains(run.Detail, "isn't there") {
		t.Errorf("no file: %+v", run)
	}

	must(t, os.WriteFile(file, []byte("# Brief"), 0o600))
	other := folderJob(filepath.Join(dir, "sub"))
	must(t, os.Mkdir(other.Cwd, 0o700))
	gone := mustSave(t, d, other)
	must(t, os.Remove(other.Cwd))
	clk.set("2026-11-13 08:00:00")
	d.runDueJobs(ctx)
	if run := lastRun(t, d, gone.ID); run.Result != core.RunFailed || !strings.Contains(run.Detail, "isn't there") {
		t.Errorf("no folder: %+v", run)
	}

	d.cfg.Hosts = nil
	if _, err := d.runJobNow(ctx, job.ID); err == nil || !strings.Contains(err.Error(), "acmd doctor") {
		t.Errorf("no claude: %v", err)
	}
	if _, err := d.runJobNow(ctx, "nope"); err == nil {
		t.Error("ran a job that isn't there")
	}
}

func TestWorktreeRun(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	d, fh, clk, _ := jobDaemon(t)
	ctx := context.Background()
	repo := t.TempDir()
	initRepo := exec.Command("git", "-C", repo, "init", "-q")
	initRepo.Env = withoutGitVars(os.Environ()) // not this repo, when a git hook runs the tests
	if out, err := initRepo.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	j := folderJob(repo)
	j.Where, j.Name = core.WhereWorktree, "Review Dependabot PRs!"
	job := mustSave(t, d, j)
	clk.set("2026-11-12 08:00:00")
	d.runDueJobs(ctx)
	if run := lastRun(t, d, job.ID); run.Result != core.RunStarted {
		t.Fatalf("run: %+v", run)
	}
	args := fh.requests()[0].Args
	if !slices.Equal(args, []string{"--model", "opus", "--worktree", "review-dependabot-prs-20261112-0800"}) {
		t.Errorf("args: %q", args)
	}
	if got := worktreeName(&core.Job{JobSpec: core.JobSpec{Name: "!!!"}}, time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC)); got != "job-20260102-0304" {
		t.Errorf("no name: %q", got)
	}
	if got := worktreeName(&core.Job{JobSpec: core.JobSpec{Name: strings.Repeat("ab ", 30)}}, time.Time{}); len(got) > 40+14 || strings.Contains(got, "--") {
		t.Errorf("long name: %q", got)
	}
}

func TestRunInSession(t *testing.T) {
	d, fh, clk, typed := jobDaemon(t)
	ctx := context.Background()
	dir := t.TempDir()
	s := &core.Session{Kind: core.KindHosted, Cwd: dir, Harness: core.HarnessClaude, ClaudeSessionID: "c-1", Status: core.StatusPaused}
	must(t, d.store.CreateSession(ctx, s))
	j := folderJob("")
	j.Where, j.SessionID = core.WhereSession, s.ID
	job := mustSave(t, d, j)
	if job.Cwd != dir {
		t.Errorf("the job takes the session's folder: %q", job.Cwd)
	}

	// Stopped: resume it with the prompt and settings.
	clk.set("2026-11-12 08:00:00")
	d.runDueJobs(ctx)
	if run := lastRun(t, d, job.ID); run.Result != core.RunStarted || run.SessionID != s.ID {
		t.Fatalf("resumed: %+v", run)
	}
	r := fh.requests()
	if len(r) != 1 || r[0].ID != s.ID || r[0].Resume != "c-1" || r[0].Prompt != j.Prompt || !slices.Equal(r[0].Args, []string{"--model", "opus"}) {
		t.Errorf("resume: %+v", r)
	}

	// Running and done with its turn: type the prompt in, pasted, then Enter.
	s, _ = d.store.Session(ctx, s.ID)
	s.Status = core.StatusDone
	must(t, d.store.UpdateSession(ctx, s))
	clk.set("2026-11-13 08:00:00")
	d.runDueJobs(ctx)
	run := lastRun(t, d, job.ID)
	if run.Result != core.RunStarted || !strings.Contains(run.Detail, "settings didn't apply") {
		t.Errorf("typed: %+v", run)
	}
	if keys := typed.to(s.ID); len(keys) != 2 || keys[0] != "\x1b[200~"+j.Prompt+"\x1b[201~" || keys[1] != "\r" {
		t.Errorf("keys: %q", keys)
	}

	// Busy: skip.
	s.Status = core.StatusBusy
	must(t, d.store.UpdateSession(ctx, s))
	clk.set("2026-11-14 08:00:00")
	d.runDueJobs(ctx)
	if run := lastRun(t, d, job.ID); run.Result != core.RunSkipped {
		t.Errorf("busy: %+v", run)
	}

	// Outside acmd, archived, deleted: fail.
	for _, tc := range []struct {
		edit func(*core.Session)
		want string
	}{
		{func(s *core.Session) { s.Status, s.Kind = core.StatusDone, core.KindExternal }, "outside acmd"},
		{func(s *core.Session) { s.Status = core.StatusArchived }, "archived"},
	} {
		s, _ = d.store.Session(ctx, s.ID)
		tc.edit(s)
		must(t, d.store.UpdateSession(ctx, s))
		if run, _ := d.runJobNow(ctx, job.ID); run.Result != core.RunFailed || !strings.Contains(run.Detail, tc.want) {
			t.Errorf("%s: %+v", tc.want, run)
		}
	}
	must(t, d.store.DeleteSession(ctx, s.ID))
	if run, _ := d.runJobNow(ctx, job.ID); run.Result != core.RunFailed || !strings.Contains(run.Detail, "deleted") {
		t.Errorf("deleted: %+v", run)
	}
}

func TestOneOffJob(t *testing.T) {
	d, fh, clk, _ := jobDaemon(t)
	ctx := context.Background()
	j := folderJob(t.TempDir())
	j.Schedule = "once on 2026-11-20 at 14:00"
	job := mustSave(t, d, j)
	if job.Words != "once on 2026-11-20 at 14:00" || job.Cron != "" {
		t.Errorf("saved: %+v", job)
	}
	clk.set("2026-11-20 14:00:30")
	d.runDueJobs(ctx)
	clk.set("2026-11-21 14:00:30")
	d.runDueJobs(ctx)
	if n := len(fh.requests()); n != 1 {
		t.Errorf("ran %d times", n)
	}
	jobs, _ := d.jobs(ctx)
	if !jobs[0].NextRun.IsZero() || jobs[0].LastRun == nil || jobs[0].LastRun.Result != core.RunStarted {
		t.Errorf("after: %+v", jobs[0])
	}
}

// The app and the CLI use the jobs through the daemon's socket.
func TestJobOps(t *testing.T) {
	p := testPaths(t)
	fh := &fakeHosts{paths: p}
	start(t, p, func(c *Config) { c.Hosts = fh; c.ScheduleEvery = time.Hour })
	c := dial(t, p)
	dir := t.TempDir()

	var check ipc.JobCheck
	bad := folderJob(dir)
	bad.Schedule = "whenever"
	must(t, c.Call(ipc.OpJobCheck, bad, &check))
	if !strings.Contains(check.Errors["schedule"], "Couldn't read") {
		t.Errorf("check: %+v", check)
	}
	if err := c.Call(ipc.OpJobSave, bad, nil); err == nil {
		t.Error("saved a bad job")
	}

	var rev ipc.WatchResponse
	must(t, c.Call(ipc.OpWatch, ipc.WatchRequest{}, &rev))
	var job core.Job
	must(t, c.Call(ipc.OpJobSave, folderJob(dir), &job))
	if job.ID == "" || job.NextRun.IsZero() {
		t.Fatalf("saved: %+v", job)
	}
	var after ipc.WatchResponse
	must(t, c.Call(ipc.OpWatch, ipc.WatchRequest{}, &after))
	if after.Rev == rev.Rev {
		t.Error("saving didn't tell the views")
	}
	snap := snapshot(t, c)
	if len(snap.Jobs) != 1 || snap.Jobs[0].ID != job.ID || snap.Jobs[0].Words != "every day at 08:00" {
		t.Errorf("snapshot: %+v", snap.Jobs)
	}

	// Edit it: the pause stays, the spec changes.
	must(t, c.Call(ipc.OpJobPause, ipc.JobRef{ID: job.ID, Paused: true}, nil))
	job.Prompt = "changed"
	var edited core.Job
	must(t, c.Call(ipc.OpJobSave, job, &edited))
	if edited.Prompt != "changed" || !edited.Paused || edited.ID != job.ID {
		t.Errorf("edited: %+v", edited)
	}
	must(t, c.Call(ipc.OpJobPause, ipc.JobRef{ID: job.ID}, nil))

	var run core.JobRun
	must(t, c.Call(ipc.OpJobRun, ipc.JobRef{ID: job.ID}, &run))
	if run.Result != core.RunStarted || run.Detail != "Run by hand." || len(fh.requests()) != 1 {
		t.Errorf("run now: %+v", run)
	}
	var runs []core.JobRun
	must(t, c.Call(ipc.OpJobRuns, ipc.JobRef{ID: job.ID}, &runs))
	if len(runs) != 1 || runs[0].ID != run.ID {
		t.Errorf("runs: %+v", runs)
	}

	var settings []harness.Setting
	must(t, c.Call(ipc.OpAgentSettings, ipc.JobRef{}, &settings))
	if len(settings) != len(harness.ClaudeSettings) {
		t.Errorf("settings: %d", len(settings))
	}
	if err := c.Call(ipc.OpAgentSettings, ipc.JobRef{Harness: "terminal"}, &settings); err == nil {
		t.Error("terminal settings")
	}

	must(t, c.Call(ipc.OpJobDelete, ipc.JobRef{ID: job.ID}, nil))
	if snap := snapshot(t, c); len(snap.Jobs) != 0 {
		t.Errorf("after delete: %+v", snap.Jobs)
	}
	if err := c.Call(ipc.OpJobDelete, ipc.JobRef{ID: job.ID}, nil); err == nil {
		t.Error("deleted twice")
	}
	if err := c.Call(ipc.OpJobSave, "not a job", nil); err == nil {
		t.Error("bad request")
	}
	if err := c.Call(ipc.OpJobRun, "nope", nil); err == nil {
		t.Error("bad ref")
	}
}
