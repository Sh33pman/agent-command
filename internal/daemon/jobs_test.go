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
	"github.com/brettmostert/agent-command/internal/remote/api"
	"github.com/brettmostert/agent-command/internal/schedule"
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
		"other harness": {func(j *core.Job) { j.Harness = "codex" }, "harness", "Only Claude Code and Antigravity"},
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
	d.cfg.AgentHelp = func(h string) string {
		if h != core.HarnessClaude {
			t.Errorf("asked %q for Claude's flags", h)
		}
		return "--model <model>\n--permission-mode"
	}
	needsEffort := folderJob(dir)
	needsEffort.Settings = map[string][]string{"effort": {"high"}}
	if check := d.checkJob(ctx, &needsEffort); !strings.Contains(check.Errors["settings"], "no --effort option") {
		t.Errorf("old claude: %v", check.Errors)
	}
	if check := d.checkJob(ctx, &ok); len(check.Errors) != 0 {
		t.Errorf("flags it has: %v", check.Errors)
	}
	d.cfg.AgentHelp = func(string) string { return "" } // couldn't run it: don't block
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
	if s.GroupID != g.ID || s.Description != "every morning" || s.Name != "Morning brief" || s.JobID != job.ID {
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
	if s.JobID != "" {
		t.Errorf("a job that continues a session didn't start it: JobID %q", s.JobID)
	}
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
	must(t, c.Call(ipc.OpAgentSettings, ipc.JobRef{Harness: core.HarnessAntigravity}, &settings))
	if len(settings) != len(harness.AntigravitySettings) || settings[2].Key != "mode" || settings[2].Flag != "--mode" {
		t.Errorf("agy's settings: %+v", settings)
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

func agyJob(dir string) core.Job {
	j := folderJob(dir)
	j.Harness, j.Settings = core.HarnessAntigravity, map[string][]string{"mode": {"accept-edits"}, "effort": {"high"}}
	return j
}

// An Antigravity job is checked against agy's settings and agy's --help.
func TestCheckAntigravityJob(t *testing.T) {
	d, _, _, _ := jobDaemon(t)
	ctx := context.Background()
	dir := t.TempDir()

	ok := agyJob(dir)
	if check := d.checkJob(ctx, &ok); len(check.Errors) != 0 {
		t.Fatalf("valid: %v", check.Errors)
	}
	claudes := agyJob(dir)
	claudes.Settings = map[string][]string{"permission-mode": {"plan"}}
	if check := d.checkJob(ctx, &claudes); !strings.Contains(check.Errors["settings"], `no setting "permission-mode"`) {
		t.Errorf("Claude's setting on agy: %v", check.Errors)
	}

	d.cfg.AgentHelp = func(h string) string {
		if h == core.HarnessAntigravity {
			return "  --model  Model\n  --effort  Reasoning effort"
		}
		return "--model --effort --permission-mode --mode"
	}
	if check := d.checkJob(ctx, &ok); check.Errors["settings"] != "The installed Antigravity CLI has no --mode option: update it, or clear Mode." {
		t.Errorf("old agy: %v", check.Errors)
	}

	// A session to continue: its agent's conversation, and its agent.
	none := &core.Session{Kind: core.KindHosted, Cwd: dir, Harness: core.HarnessAntigravity, Status: core.StatusDone}
	must(t, d.store.CreateSession(ctx, none))
	j := folderJob("")
	j.Where, j.SessionID, j.Settings = core.WhereSession, none.ID, nil
	if check := d.checkJob(ctx, &j); check.Errors["session"] != "That session has no Antigravity conversation to continue." {
		t.Errorf("no conversation: %v", check.Errors)
	}
	claude := &core.Session{Kind: core.KindHosted, Cwd: dir, Harness: core.HarnessClaude, Status: core.StatusDone}
	must(t, d.store.CreateSession(ctx, claude))
	j.SessionID = claude.ID
	if check := d.checkJob(ctx, &j); check.Errors["session"] != "That session has no Claude conversation to continue." {
		t.Errorf("no Claude conversation: %v", check.Errors)
	}
	none.ClaudeSessionID = "conv-1"
	must(t, d.store.UpdateSession(ctx, none))
	j.SessionID = none.ID
	if check := d.checkJob(ctx, &j); len(check.Errors) != 0 || j.Harness != core.HarnessAntigravity || j.Cwd != dir {
		t.Errorf("agy session: %v, harness %q", check.Errors, j.Harness)
	}
	claude.ClaudeSessionID = "c-1"
	must(t, d.store.UpdateSession(ctx, claude))
	j.SessionID = claude.ID
	if check := d.checkJob(ctx, &j); len(check.Errors) != 0 || j.Harness != core.HarnessClaude {
		t.Errorf("back to a Claude session: %v, harness %q", check.Errors, j.Harness)
	}
}

// An Antigravity job runs agy: in a new session with its flags and the prompt,
// or in its session's conversation, resumed with the prompt.
func TestRunAntigravityJob(t *testing.T) {
	d, fh, clk, typed := jobDaemon(t)
	ctx := context.Background()
	dir := t.TempDir()
	job := mustSave(t, d, agyJob(dir))
	clk.set("2026-11-12 08:00:00")
	d.runDueJobs(ctx)
	run := lastRun(t, d, job.ID)
	if run.Result != core.RunStarted {
		t.Fatalf("run: %+v", run)
	}
	r := fh.requests()
	if len(r) != 1 || r[0].Harness != core.HarnessAntigravity || r[0].Prompt != job.Prompt || r[0].ClaudeSession != "" ||
		!slices.Equal(r[0].Args, []string{"--effort", "high", "--mode", "accept-edits"}) {
		t.Errorf("host: %+v", r)
	}
	if s, err := d.store.Session(ctx, run.SessionID); err != nil || !s.IsAntigravity() {
		t.Errorf("session: %+v %v", s, err)
	}

	s := &core.Session{Kind: core.KindHosted, Cwd: dir, Harness: core.HarnessAntigravity, ClaudeSessionID: "conv-1", Status: core.StatusPaused}
	must(t, d.store.CreateSession(ctx, s))
	j := agyJob("")
	j.Where, j.SessionID, j.Harness = core.WhereSession, s.ID, ""
	cont := mustSave(t, d, j)
	if cont.Harness != core.HarnessAntigravity {
		t.Errorf("the job takes its session's agent: %q", cont.Harness)
	}
	clk.set("2026-11-13 08:00:00")
	d.runDueJobs(ctx)
	r = fh.requests()
	if run := lastRun(t, d, cont.ID); run.Result != core.RunStarted || len(r) != 2 { // the first job's run is still starting
		t.Fatalf("resumed: %+v (%d starts)", run, len(r))
	}
	if got := r[len(r)-1]; got.ID != s.ID || got.Harness != core.HarnessAntigravity || got.Resume != "conv-1" || got.Prompt != j.Prompt ||
		!slices.Equal(got.Args, []string{"--effort", "high", "--mode", "accept-edits"}) {
		t.Errorf("resume: %+v", got)
	}

	// Done with its turn: typed in, as for Claude.
	s, _ = d.store.Session(ctx, s.ID)
	s.Status = core.StatusDone
	must(t, d.store.UpdateSession(ctx, s))
	if run, _ := d.runJobNow(ctx, cont.ID); run.Result != core.RunStarted {
		t.Errorf("typed: %+v", run)
	}
	if keys := typed.to(s.ID); len(keys) != 2 || keys[0] != "\x1b[200~"+j.Prompt+"\x1b[201~" || keys[1] != "\r" {
		t.Errorf("keys: %q", keys)
	}

	// A job saved for Claude, before jobs took the session's agent, doesn't send
	// Claude's flags to agy.
	stale, _ := d.store.Job(ctx, cont.ID)
	stale.Harness, stale.Settings = "", map[string][]string{"permission-mode": {"plan"}}
	must(t, d.store.SaveJob(ctx, stale))
	if run, _ := d.runJobNow(ctx, cont.ID); run.Result != core.RunFailed ||
		run.Detail != "The session runs Antigravity, but the job's settings are for Claude: edit the job." {
		t.Errorf("stale: %+v", run)
	}

	d.cfg.Hosts = nil
	if run, _ := d.runJobNow(ctx, job.ID); run.Detail != "Antigravity CLI isn't available: run `acmd doctor`." {
		t.Errorf("no hosts: %+v", run)
	}
	other, _ := d.store.Job(ctx, job.ID)
	other.Harness = "codex"
	must(t, d.store.SaveJob(ctx, other))
	if run, _ := d.runJobNow(ctx, job.ID); run.Detail != "acmd can't schedule codex." {
		t.Errorf("other: %+v", run)
	}
}

func TestHasFlag(t *testing.T) {
	help := "  --model   Model\n  --mode    Mode (accept-edits)\n  --add-dir"
	for flag, want := range map[string]bool{"--model": true, "--mode": true, "--add-dir": true, "--add": false, "--mod": false, "--effort": false} {
		if got := hasFlag(help, flag); got != want {
			t.Errorf("%s: %v", flag, got)
		}
	}
	if hasFlag("  --model  Model", "--mode") {
		t.Error("--mode found in --model")
	}
}

// The schedule form goes through job.check: the daemon maps it to and from
// cron, so the app has none of that logic.
func TestJobCheckTakesTheForm(t *testing.T) {
	p := testPaths(t)
	start(t, p, func(c *Config) { c.Hosts = &fakeHosts{paths: p}; c.ScheduleEvery = time.Hour; c.Location = time.UTC })
	c := dial(t, p)
	dir := t.TempDir()
	var check ipc.JobCheck
	req := ipc.JobCheckRequest{Job: folderJob(dir), Form: &schedule.Form{Kind: schedule.FormWeekly, Hour: 9, Minute: 30, WeekDays: []int{1, 3}}}
	req.Schedule = "" // the app sends the form, not cron
	must(t, c.Call(ipc.OpJobCheck, req, &check))
	if check.Cron != "30 9 * * 1,3" || check.Form == nil || check.Form.Kind != schedule.FormWeekly || len(check.Errors) != 0 {
		t.Errorf("weekly: %+v %+v", check, check.Form)
	}
	// A schedule as typed wins over a form sent with it (the CLI's edits).
	typed := req
	typed.Schedule = "0 8 * * *"
	must(t, c.Call(ipc.OpJobCheck, typed, &check))
	if check.Cron != "0 8 * * *" {
		t.Errorf("typed = %+v", check)
	}
	// Saved from the form; the snapshot's job carries it.
	var saved core.Job
	must(t, c.Call(ipc.OpJobSave, req, &saved))
	if saved.Cron != "30 9 * * 1,3" || saved.Form == nil || saved.Form.Kind != schedule.FormWeekly {
		t.Errorf("saved = %+v", saved)
	}
	if jobs := snapshot(t, c).Jobs; len(jobs) != 1 || jobs[0].Form == nil || len(jobs[0].Form.WeekDays) != 2 {
		t.Errorf("snapshot's job = %+v", jobs)
	}
	// Typed as words, it comes back as the form.
	j := folderJob(dir)
	j.Schedule = "weekdays at 07:15"
	must(t, c.Call(ipc.OpJobCheck, ipc.JobCheckRequest{Job: j}, &check))
	if check.Form == nil || check.Form.Kind != schedule.FormWeekdays || check.Form.Hour != 7 || check.Form.Minute != 15 {
		t.Errorf("weekdays: %+v", check.Form)
	}
	// A form that doesn't parse stays as it was, with the error.
	req.Form = &schedule.Form{Kind: schedule.FormCustom, Cron: "nonsense"}
	req.Schedule = ""
	must(t, c.Call(ipc.OpJobCheck, req, &check))
	if check.Errors["schedule"] == "" || check.Form == nil || check.Form.Cron != "nonsense" {
		t.Errorf("custom: %+v", check)
	}
}

// Prompt files: a saved job's own, by id, and a draft's Markdown or text
// file. Nothing else.
func TestPromptFileOps(t *testing.T) {
	p := testPaths(t)
	start(t, p, func(c *Config) { c.Hosts = &fakeHosts{paths: p}; c.ScheduleEvery = time.Hour })
	c := dial(t, p)
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "brief.md"), []byte("# Brief"), 0o600))
	must(t, os.WriteFile(filepath.Join(dir, "secret.key"), []byte("k"), 0o600))

	// A draft's file, relative to its folder.
	var f ipc.PromptFile
	must(t, c.Call(ipc.OpJobPromptRead, ipc.PromptFileRef{Cwd: dir, Path: "brief.md"}, &f))
	if f.Text != "# Brief" || !f.Exists || f.Path != filepath.Join(dir, "brief.md") {
		t.Errorf("read = %+v", f)
	}
	must(t, c.Call(ipc.OpJobPromptRead, ipc.PromptFileRef{Cwd: dir, Path: "new.md"}, &f))
	if f.Exists || f.Text != "" {
		t.Errorf("a new file = %+v", f)
	}
	must(t, c.Call(ipc.OpJobPromptWrite, ipc.PromptFileRef{Cwd: dir, Path: "new.md", Text: "# New"}, &f))
	if b, _ := os.ReadFile(filepath.Join(dir, "new.md")); string(b) != "# New" {
		t.Errorf("written = %q", b)
	}
	for _, bad := range []ipc.PromptFileRef{
		{Cwd: dir, Path: "secret.key"}, // not a prompt file
		{Path: "brief.md"},             // relative, with no folder
		{},                             // nothing named
		{Cwd: dir, Path: "../" + filepath.Base(dir) + "/secret.key"},
	} {
		if err := c.Call(ipc.OpJobPromptRead, bad, nil); err == nil {
			t.Errorf("read %+v", bad)
		}
		if err := c.Call(ipc.OpJobPromptWrite, bad, nil); err == nil {
			t.Errorf("wrote %+v", bad)
		}
	}
	link := filepath.Join(dir, "link.md")
	must(t, os.Symlink(filepath.Join(dir, "secret.key"), link))
	if err := c.Call(ipc.OpJobPromptRead, ipc.PromptFileRef{Cwd: dir, Path: "link.md"}, nil); err == nil {
		t.Error("followed a link")
	}

	// A saved job's own file, by id: the path it names is ignored.
	j := folderJob(dir)
	j.Prompt, j.PromptFile = "", "brief.md"
	var job core.Job
	must(t, c.Call(ipc.OpJobSave, j, &job))
	must(t, c.Call(ipc.OpJobPromptRead, ipc.PromptFileRef{JobID: job.ID, Path: "/etc/passwd"}, &f))
	if f.Text != "# Brief" {
		t.Errorf("by id = %+v", f)
	}
	must(t, c.Call(ipc.OpJobPromptWrite, ipc.PromptFileRef{JobID: job.ID, Text: "# Changed"}, &f))
	if b, _ := os.ReadFile(filepath.Join(dir, "brief.md")); string(b) != "# Changed" {
		t.Errorf("by id written = %q", b)
	}
	inline := folderJob(dir)
	must(t, c.Call(ipc.OpJobSave, inline, &job))
	if err := c.Call(ipc.OpJobPromptRead, ipc.PromptFileRef{JobID: job.ID}, nil); err == nil {
		t.Error("a job whose prompt is inline has no file")
	}
	if err := c.Call(ipc.OpJobPromptRead, ipc.PromptFileRef{JobID: "nope"}, nil); err == nil {
		t.Error("a job that isn't there")
	}
	if err := c.Call(ipc.OpJobPromptWrite, ipc.PromptFileRef{Cwd: dir, Path: "big.md", Text: strings.Repeat("x", maxPromptFile+1)}, nil); err == nil {
		t.Error("over 1 MB")
	}
}

// This machine's folders, served as the Remote API serves a host's.
func TestLocalFolderOps(t *testing.T) {
	p := testPaths(t)
	home := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(home, "proj", "sub"), 0o750))
	must(t, os.WriteFile(filepath.Join(home, "proj", "notes.md"), nil, 0o600))
	must(t, os.WriteFile(filepath.Join(home, "proj", "main.go"), nil, 0o600))
	start(t, p, func(c *Config) { c.Home = home })
	c := dial(t, p)
	var list api.FolderList
	must(t, c.Call(ipc.OpFoldersList, api.FolderPath{Path: "~/proj", Files: true}, &list))
	if len(list.Folders) != 1 || list.Folders[0].Name != "sub" || len(list.Files) != 1 || list.Files[0].Name != "notes.md" {
		t.Errorf("list = %+v", list)
	}
	must(t, os.WriteFile(filepath.Join(home, "proj", "mcp.json"), nil, 0o600))
	list = api.FolderList{}
	must(t, c.Call(ipc.OpFoldersList, api.FolderPath{Path: "~/proj", Files: true, Exts: []string{".JSON"}}, &list))
	if len(list.Files) != 1 || list.Files[0].Name != "mcp.json" {
		t.Errorf("json files = %+v", list.Files)
	}
	list = api.FolderList{}
	must(t, c.Call(ipc.OpFoldersList, api.FolderPath{Path: "~/proj"}, &list))
	if len(list.Files) != 0 {
		t.Error("files without asking")
	}
	var made api.Folder
	must(t, c.Call(ipc.OpFoldersMkdir, api.FolderPath{Path: "proj/new"}, &made))
	if made.Path != filepath.Join(home, "proj", "new") {
		t.Errorf("made = %+v", made)
	}
	must(t, c.Call(ipc.OpEvent, hookEvent("SessionStart", `{"session_id":"f1","source":"startup","cwd":"`+filepath.Join(home, "proj")+`"}`, time.Now(), ipc.HookContext{}), nil))
	must(t, c.Call(ipc.OpFoldersRecent, nil, &list))
	if len(list.Folders) != 1 || list.Folders[0].Name != "proj" {
		t.Errorf("recent = %+v", list)
	}
	if err := c.Call(ipc.OpFoldersList, api.FolderPath{Path: "~/nope"}, nil); err == nil {
		t.Error("listed a folder that isn't there")
	}
	if err := c.Call(ipc.OpFoldersMkdir, api.FolderPath{}, nil); err == nil {
		t.Error("made nothing")
	}
}
