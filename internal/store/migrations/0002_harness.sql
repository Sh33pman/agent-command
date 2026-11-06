-- 0002: what a session runs (docs/v2/architecture.md §3).

ALTER TABLE sessions ADD COLUMN harness TEXT NOT NULL DEFAULT 'claude';
