package core

import "time"

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
	Harness     string `json:"harness,omitempty"` // "" = claude
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
	// RunMissed runs a run the Mac slept through when it wakes, instead of only
	// showing it as missed.
	RunMissed bool `json:"run_missed,omitempty"`
	// Wake wakes the Mac shortly before each run, once acmd may (acmd schedule
	// wake on). Waking for every job is a setting of its own.
	Wake bool `json:"wake,omitempty"`
}

// Job is a scheduled job: its spec, its schedule as the daemon reads it, and
// what's happened with it.
type Job struct {
	ID string `json:"id"`
	JobSpec
	Cron  string    `json:"cron,omitempty"` // "" for a one-off
	Once  time.Time `json:"once,omitzero"`  // a one-off's time
	Words string    `json:"words,omitempty"`
	// Summary is the schedule in English, for reading: Words, or a plainer
	// account of cron that has none.
	Summary   string    `json:"summary,omitempty"`
	Paused    bool      `json:"paused,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// LastDue is the latest run time dealt with (run, skipped or missed): the
	// next run is the first one after it.
	LastDue time.Time `json:"last_due,omitzero"`
	// NextRun is when it runs next (zero: never again, or paused).
	NextRun time.Time `json:"next_run,omitzero"`
	LastRun *JobRun   `json:"last_run,omitempty"`
}

// Results of a job's run.
const (
	RunStarted = "started" // its session started (or the prompt was sent)
	RunSkipped = "skipped" // the last run, or the session, was still busy
	RunMissed  = "missed"  // the Mac was asleep or acmd wasn't running
	RunFailed  = "failed"  // it couldn't start: Detail says why
)

// JobRun is one time a job was due.
type JobRun struct {
	ID        int64     `json:"id"`
	JobID     string    `json:"job_id"`
	Due       time.Time `json:"due"`
	At        time.Time `json:"at"`
	Result    string    `json:"result"`
	SessionID string    `json:"session_id,omitempty"`
	Detail    string    `json:"detail,omitempty"`
}
