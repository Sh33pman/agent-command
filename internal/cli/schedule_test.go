package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

func TestScheduleCommands(t *testing.T) {
	env(t)
	dir := t.TempDir()
	t.Chdir(dir)

	if out := mustRun(t, "schedule", "list"); !strings.Contains(out, "No scheduled jobs") {
		t.Errorf("empty list: %q", out)
	}
	out := mustRun(t, "schedule", "add", "Morning triage", "--cron", " 0  9 * * 1-5", "--prompt", "Triage new issues",
		"--set", "model=opus", "--set", "allowed-tools=Read", "--set", "allowed-tools=Bash(git log:*)", "--description", "issues")
	if !strings.Contains(out, `Added "Morning triage": weekdays at 09:00 (0 9 * * 1-5). Next run `) {
		t.Errorf("add: %q", out)
	}
	out = mustRun(t, "schedule", "list")
	if !strings.Contains(out, "Morning triage") || !strings.Contains(out, "weekdays at 09:00 (0 9 * * 1-5)") {
		t.Errorf("list: %q", out)
	}

	out = mustRun(t, "schedule", "show", "morning")
	for _, want := range []string{"issues", "a new session in " + dir, "allowed-tools", "Read, Bash(git log:*)",
		"opus", "Triage new issues"} {
		if !strings.Contains(out, want) {
			t.Errorf("show is missing %q:\n%s", want, out)
		}
	}

	// Edit changes only what's given.
	notes := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(notes, []byte("# Notes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "schedule", "edit", "Morning triage", "--cron", "*/7 * * * *", "--prompt-file", "notes.md",
		"--unset", "model", "--set", "effort=high", "--name", "Triage", "--run-missed")
	j := job(t, "Triage")
	if j.Cron != "*/7 * * * *" || j.PromptFile != notes || j.Prompt != "" || j.Description != "issues" || !j.RunMissed ||
		j.Settings["model"] != nil || j.Settings["effort"][0] != "high" || len(j.Settings["allowed-tools"]) != 2 {
		t.Errorf("edit: %+v", j)
	}
	if out := mustRun(t, "schedule", "show", "Triage"); !strings.Contains(out, "read from "+notes) {
		t.Errorf("show prompt file: %q", out)
	}

	// A worktree needs a repo; a one-off goes in as one.
	if _, err := acmd(t, "schedule", "edit", "Triage", "--worktree", dir); err == nil {
		t.Error("a worktree outside a repo")
	}
	at := time.Now().Add(48 * time.Hour).Truncate(time.Minute)
	mustRun(t, "schedule", "edit", "Triage", "--once", at.Format("2006-01-02 15:04"))
	if j := job(t, "Triage"); j.Cron != "" || !j.Once.Equal(at) {
		t.Errorf("once: %+v", j)
	}
	mustRun(t, "schedule", "edit", "Triage", "--description", "still once")
	if j := job(t, "Triage"); !j.Once.Equal(at) {
		t.Errorf("editing kept the one-off? %+v", j)
	}

	mustRun(t, "schedule", "pause", "Triage")
	if out := mustRun(t, "schedule", "list"); !strings.Contains(out, "paused") {
		t.Errorf("paused: %q", out)
	}
	mustRun(t, "schedule", "resume", "Triage")
	if j := job(t, "Triage"); j.Paused {
		t.Error("resumed")
	}

	// The test daemon has no hosts, so a run fails, and says why.
	if _, err := acmd(t, "schedule", "run", "Triage"); err == nil || !strings.Contains(err.Error(), "Claude isn't available") {
		t.Errorf("run: %v", err)
	}
	if out := mustRun(t, "schedule", "runs", "Triage"); !strings.Contains(out, "failed") {
		t.Errorf("runs: %q", out)
	}
	if out := mustRun(t, "schedule", "show", "Triage"); !strings.Contains(out, "DUE") {
		t.Errorf("show runs: %q", out)
	}

	if _, err := acmd(t, "schedule", "delete", "Triage"); err == nil {
		t.Error("deleted without --yes")
	}
	if out := mustRun(t, "schedule", "delete", "Triage", "--yes"); !strings.Contains(out, `Deleted "Triage"`) {
		t.Errorf("delete: %q", out)
	}
	if out := mustRun(t, "jobs", "list"); !strings.Contains(out, "No scheduled jobs") {
		t.Errorf("after delete: %q", out)
	}
}

func TestScheduleAddFromStdinAndAFolder(t *testing.T) {
	env(t)
	dir := t.TempDir()
	root := NewRootCmd("acmd", &strings.Builder{}, &strings.Builder{})
	root.SetIn(strings.NewReader("From **stdin**\n"))
	root.SetArgs([]string{"schedule", "add", "Nightly", "--cron", "30 2 * * *", "--prompt", "-", "--folder", dir})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if j := job(t, "Nightly"); j.Prompt != "From **stdin**\n" || j.Cwd != dir || j.Where != core.WhereFolder {
		t.Errorf("stdin: %+v", j)
	}
}

func TestScheduleRejects(t *testing.T) {
	env(t)
	t.Chdir(t.TempDir())
	mustRun(t, "schedule", "add", "Alpha one", "--cron", "0 9 * * *", "--prompt", "a")
	mustRun(t, "schedule", "add", "Alpha two", "--cron", "0 9 * * *", "--prompt", "a")
	for _, args := range [][]string{
		{"add", "x", "--prompt", "p"},                                // no schedule
		{"add", "x", "--cron", "0 9 * * *"},                          // no prompt
		{"add", "x", "--cron", "weekdays at 09:00", "--prompt", "p"}, // cron only
		{"add", "x", "--cron", "0 9 * *", "--prompt", "p"},           // four fields
		{"add", "x", "--once", "tomorrow", "--prompt", "p"},          // not a time
		{"add", "x", "--once", "2020-01-01 09:00", "--prompt", "p"},  // passed
		{"add", "x", "--cron", "0 9 * * *", "--once", "2030-01-01 09:00", "--prompt", "p"},
		{"add", "x", "--cron", "0 9 * * *", "--prompt", "p", "--set", "nokey"},
		{"add", "x", "--cron", "0 9 * * *", "--prompt", "p", "--set", "effort=huge"},
		{"add", "x", "--cron", "0 9 * * *", "--prompt", "p", "--set", "shade=red"},
		{"add", "x", "--cron", "0 9 * * *", "--prompt", "p", "--session", "nope"},
		{"add", "x", "--cron", "0 9 * * *", "--prompt", "p", "--folder", "/no/such/folder"},
		{"show", "Alpha"}, // two match
		{"show", "Beta"},
		{"run", "nope"},
		{"pause", "nope"},
		{"runs", "nope"},
		{"delete", "nope", "--yes"},
		{"edit", "nope"},
		{"explain", "not cron"},
	} {
		if _, err := acmd(t, append([]string{"schedule"}, args...)...); err == nil {
			t.Errorf("schedule %v worked", args)
		}
	}
}

func TestScheduleExplainAndSettings(t *testing.T) {
	out := mustRun(t, "schedule", "explain", "0", "9", "*", "*", "1-5", "-n", "2")
	if lines := strings.Split(strings.TrimSpace(out), "\n"); lines[0] != "0 9 * * 1-5: weekdays at 09:00" || len(lines) != 3 {
		t.Errorf("explain: %q", out)
	}
	if out := mustRun(t, "schedule", "explain", "*/7 * * * *", "-n", "0"); !strings.HasPrefix(out, "*/7 * * * *: ") {
		t.Errorf("explain cron with no words: %q", out)
	}
	out = mustRun(t, "schedule", "settings")
	for _, want := range []string{"permission-mode", "--allowedTools", "low, medium, high"} {
		if !strings.Contains(out, want) {
			t.Errorf("settings is missing %q:\n%s", want, out)
		}
	}
}

func TestJobText(t *testing.T) {
	if s := jobSchedule(&core.Job{Cron: "*/7 * * * *", Summary: "*/7 * * * *"}); s != "*/7 * * * *" {
		t.Error(s)
	}
	if s := jobState(&core.Job{}); s != "done" {
		t.Error(s)
	}
	if s := lastText(nil); s != "-" {
		t.Error(s)
	}
	snap := ipc.Snapshot{Sessions: []*core.Session{{ID: "s1", Name: "docs"}}}
	if s := sessionText(snap, "s1", " in "); s != ` in "docs"` {
		t.Error(s)
	}
	if s := sessionText(snap, "s2", ""); s != "s2" {
		t.Error(s)
	}
	home, _ := os.UserHomeDir()
	if p, _ := absPath("~/x"); p != filepath.Join(home, "x") {
		t.Error(p)
	}
	var b strings.Builder
	writeRuns(&b, snap, nil)
	if b.String() != "No runs yet.\n" {
		t.Error(b.String())
	}
	var sb strings.Builder
	showJob(&sb, &core.Job{JobSpec: core.JobSpec{Name: "j", Where: core.WhereSession, SessionID: "s1", Prompt: "p"}}, snap, nil)
	if !strings.Contains(sb.String(), `the session "docs"`) {
		t.Error(sb.String())
	}
	sb.Reset()
	showJob(&sb, &core.Job{JobSpec: core.JobSpec{Name: "j", Where: core.WhereWorktree, Cwd: "/r", Prompt: "p"}}, snap, nil)
	if !strings.Contains(sb.String(), "a new worktree of /r") {
		t.Error(sb.String())
	}
}

// job is the job named name, from the daemon.
func job(t *testing.T, name string) *core.Job {
	t.Helper()
	var found *core.Job
	err := withClient(t.Context(), func(_ *client, snap ipc.Snapshot) error {
		var err error
		found, err = resolveJob(snap, name)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}
