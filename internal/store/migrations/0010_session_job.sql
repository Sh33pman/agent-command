-- 0010: the scheduled job that started a session, so the app can mark it.
-- Sessions a job already started (in a folder or a new worktree) get their
-- job from its runs; a job that continues an existing session didn't start it.

ALTER TABLE sessions ADD COLUMN job_id TEXT NOT NULL DEFAULT '';

UPDATE sessions SET job_id = coalesce((
    SELECT r.job_id FROM job_runs r JOIN jobs j ON j.id = r.job_id
    WHERE r.session_id = sessions.id AND r.result = 'started'
      AND json_extract(j.spec, '$.where') IN ('folder', 'worktree')
    ORDER BY r.id LIMIT 1), '');
