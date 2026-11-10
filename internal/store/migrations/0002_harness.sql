-- 0002: what a session runs.

ALTER TABLE sessions ADD COLUMN harness TEXT NOT NULL DEFAULT 'claude';
