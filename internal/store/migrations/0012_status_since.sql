-- 0012: when each session's status last changed, for "next that needs you":
-- the one waiting longest comes first. Until now only the last activity was
-- kept, which moves on every event; it's the best guess for existing rows.

ALTER TABLE sessions ADD COLUMN status_since INTEGER NOT NULL DEFAULT 0;

UPDATE sessions SET status_since = CASE WHEN last_event_at != 0 THEN last_event_at ELSE created_at END;
