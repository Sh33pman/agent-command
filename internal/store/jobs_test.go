package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
)

func openAt(t *testing.T, now *time.Time) *Store {
	t.Helper()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "state.db"), WithClock(func() time.Time { return *now }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestJobs(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	s := openAt(t, &now)

	j := &core.Job{JobSpec: core.JobSpec{Name: "morning", Schedule: "weekdays at 08:00", Prompt: "# Brief\n\n- calendar",
		Where: core.WhereFolder, Cwd: "/w/notes", Settings: map[string][]string{"model": {"opus"}, "add-dir": {"/a", "/b"}}},
		Cron: "0 8 * * 1-5", Words: "weekdays at 08:00"}
	if err := s.SaveJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	if j.ID == "" || !j.CreatedAt.Equal(now) || !j.LastDue.Equal(now) {
		t.Fatalf("new job: %+v", j)
	}
	other := &core.Job{JobSpec: core.JobSpec{Name: "a first", Schedule: "every hour", Where: core.WhereFolder, Cwd: "/w"}, Cron: "0 * * * *"}
	if err := s.SaveJob(ctx, other); err != nil {
		t.Fatal(err)
	}

	got, err := s.Job(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Prompt != j.Prompt || got.Cron != j.Cron || got.Settings["add-dir"][1] != "/b" || got.LastRun != nil {
		t.Errorf("read back: %+v", got)
	}
	jobs, err := s.Jobs(ctx)
	if err != nil || len(jobs) != 2 || jobs[0].Name != "a first" {
		t.Fatalf("jobs: %v %v", jobs, err)
	}

	// Runs: the latest shows on the job, and moves LastDue on.
	now = now.Add(24 * time.Hour)
	due := time.Date(2026, 11, 13, 8, 0, 0, 0, time.UTC)
	if err := s.RecordRun(ctx, &core.JobRun{JobID: j.ID, Due: due, Result: core.RunStarted, SessionID: "S1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordRun(ctx, &core.JobRun{JobID: j.ID, Due: due.Add(-time.Hour), Result: core.RunMissed}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Job(ctx, j.ID)
	if got.LastRun == nil || got.LastRun.Result != core.RunMissed || !got.LastDue.Equal(due) {
		t.Errorf("after runs: %+v %+v", got, got.LastRun)
	}
	jobs, _ = s.Jobs(ctx)
	if jobs[1].LastRun == nil || jobs[1].LastRun.Result != core.RunMissed || jobs[0].LastRun != nil {
		t.Errorf("jobs' last runs: %+v %+v", jobs[0].LastRun, jobs[1].LastRun)
	}
	runs, _ := s.JobRuns(ctx, j.ID, 0)
	if len(runs) != 2 || runs[0].Result != core.RunMissed || runs[1].SessionID != "S1" || !runs[1].At.Equal(now) {
		t.Errorf("runs: %+v", runs)
	}

	// Editing without changing the schedule keeps LastDue; a new schedule starts from now.
	got.Prompt = "changed"
	if err := s.SaveJob(ctx, got); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.Job(ctx, j.ID); again.Prompt != "changed" || !again.LastDue.Equal(due) || !again.CreatedAt.Equal(j.CreatedAt) {
		t.Errorf("edited: %+v", again)
	}
	got.Cron = "0 9 * * *"
	now = now.Add(time.Hour)
	if err := s.SaveJob(ctx, got); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.Job(ctx, j.ID); !again.LastDue.Equal(now) {
		t.Errorf("rescheduled: last due %s, want %s", again.LastDue, now)
	}

	// Pausing; resuming counts from now.
	if err := s.SetJobPaused(ctx, j.ID, true); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.Job(ctx, j.ID); !again.Paused {
		t.Error("not paused")
	}
	now = now.Add(48 * time.Hour)
	if err := s.SetJobPaused(ctx, j.ID, false); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.Job(ctx, j.ID); again.Paused || !again.LastDue.Equal(now) {
		t.Errorf("resumed: %+v", again)
	}

	for _, err := range []error{
		s.SetJobPaused(ctx, "nope", true),
		s.DeleteJob(ctx, "nope"),
		s.SaveJob(ctx, &core.Job{ID: "nope"}),
	} {
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("missing job: %v", err)
		}
	}
	if _, err := s.Job(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing job: %v", err)
	}

	// Deleting takes its runs too.
	if err := s.DeleteJob(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	if runs, _ := s.JobRuns(ctx, j.ID, 0); len(runs) != 0 {
		t.Errorf("runs left: %v", runs)
	}
}

func TestJobRunsAreCapped(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	s := openAt(t, &now)
	j := &core.Job{JobSpec: core.JobSpec{Name: "often", Where: core.WhereFolder, Cwd: "/w"}, Cron: "* * * * *"}
	if err := s.SaveJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < runsKept+5; i++ {
		if err := s.RecordRun(ctx, &core.JobRun{JobID: j.ID, Due: now.Add(time.Duration(i) * time.Minute), Result: core.RunStarted}); err != nil {
			t.Fatal(err)
		}
	}
	runs, _ := s.JobRuns(ctx, j.ID, 0)
	if len(runs) != runsKept || !runs[0].Due.Equal(now.Add((runsKept+4)*time.Minute)) {
		t.Errorf("kept %d, newest due %s", len(runs), runs[0].Due)
	}
	if few, _ := s.JobRuns(ctx, j.ID, 3); len(few) != 3 {
		t.Errorf("limit: %d", len(few))
	}
}
