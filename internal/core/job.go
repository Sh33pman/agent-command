package core

import (
	"time"

	"github.com/brettmostert/agent-command/internal/schedule"
)

// Where a scheduled job runs.
const (
	// WhereFolder starts a new session in the job's folder.
	WhereFolder = "folder"
	// WhereWorktree starts a new session in a fresh git worktree of the job's
	// folder's repo, so a run can't collide with your own work there.
	WhereWorktree = "worktree"
	// WhereSession continues an existing session's conversation.
	WhereSession = "session"
)

// JobSpec is what you set on a scheduled job.
type JobSpec struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	GroupID     string `json:"group_id,omitempty"`
	HarnessID   string `json:"harness_id,omitempty"` // the harness it runs in; "" is the default harness
	// Schedule is as it was typed: cron, or words. The daemon keeps it in its
	// canonical form (the words when there are any, else the cron).
	Schedule string `json:"schedule"`
	// Prompt is the prompt's Markdown. PromptFile, when set, is a Markdown file
	// read at each run instead (relative to Cwd if it isn't absolute).
	Prompt     string `json:"prompt,omitempty"`
	PromptFile string `json:"prompt_file,omitempty"`
	Where      string `json:"where"`                // WhereFolder, WhereWorktree or WhereSession
	Cwd        string `json:"cwd,omitempty"`        // the folder, or the repo for a worktree
	SessionID  string `json:"session_id,omitempty"` // the acmd session, for WhereSession
	// Settings are the agent's settings by key (harness.Settings), each with
	// its values: one for most, several for lists.
	Settings map[string][]string `json:"settings,omitempty"`
	// RunMissed runs a run the host slept through when it wakes, instead of only
	// showing it as missed.
	RunMissed bool `json:"run_missed,omitempty"`
	// Wake wakes the host shortly before each run, once acmd may (acmd schedule
	// wake on). Waking for every job is a setting of its own.
	Wake bool `json:"wake,omitempty"`
}

// Job is a scheduled job: its spec, its schedule as the daemon reads it, and
// what's happened with it.
type Job struct {
	ID string `json:"id"`
	// Version is a hash of what the job form edits (its spec and schedule),
	// as the client loaded it. A job.save that sends it back is refused with
	// errcode.Conflict when the job changed since; one with none applies.
	Version string `json:"version,omitempty"`
	JobSpec
	Cron  string    `json:"cron,omitempty"` // "" for a one-off
	Once  time.Time `json:"once,omitzero"`  // a one-off's time
	Words string    `json:"words,omitempty"`
	// Summary is the schedule in English, for reading: Words, or a plainer
	// account of cron that has none.
	Summary   string    `json:"summary,omitempty"`
	Paused    bool      `json:"paused,omitempty"`
	CreatedAt time.Time `json:"created_at,omitzero"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
	// LastDue is the latest run time dealt with (run, skipped or missed): the
	// next run is the first one after it.
	LastDue time.Time `json:"last_due,omitzero"`
	// NextRun is when it runs next (zero: never again, or paused).
	NextRun time.Time `json:"next_run,omitzero"`
	LastRun *JobRun   `json:"last_run,omitempty"`
	// Form is the schedule as a form's controls show it, for clients: the
	// daemon fills it in from Cron or Once. Nothing stores it.
	Form *schedule.Form `json:"form,omitempty"`
}

// Results of a job's run.
const (
	RunStarted = "started" // its session started (or the prompt was sent)
	RunSkipped = "skipped" // the last run, or the session, was still busy
	RunMissed  = "missed"  // the host was asleep or acmd wasn't running
	RunFailed  = "failed"  // it couldn't start: Detail says why
)

// JobRun is one time a job was due.
type JobRun struct {
	ID        int64     `json:"id"`
	JobID     string    `json:"job_id"`
	Due       time.Time `json:"due,omitzero"`
	At        time.Time `json:"at,omitzero"`
	Result    string    `json:"result"`
	SessionID string    `json:"session_id,omitempty"`
	Detail    string    `json:"detail,omitempty"`
}
