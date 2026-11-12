-- Scheduled jobs. The spec is JSON (core.JobSpec), so a new setting needs no
-- migration; what the scheduler queries has its own columns.
CREATE TABLE jobs (
    id         TEXT    PRIMARY KEY,
    spec       TEXT    NOT NULL,
    cron       TEXT    NOT NULL DEFAULT '',
    once_at    INTEGER NOT NULL DEFAULT 0,
    paused     INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    last_due   INTEGER NOT NULL DEFAULT 0
);

-- Each time a job was due: started, skipped, missed or failed.
CREATE TABLE job_runs (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id     TEXT    NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    due        INTEGER NOT NULL,
    at         INTEGER NOT NULL,
    result     TEXT    NOT NULL,
    session_id TEXT    NOT NULL DEFAULT '',
    detail     TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX job_runs_job ON job_runs (job_id, id);
