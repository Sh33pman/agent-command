-- 0009: sessions added from Remote hosts. host is the known host's name
-- ('' for a session on this machine), remote_id the session's id there, and
-- host_gone says the host no longer offers it.

ALTER TABLE sessions ADD COLUMN host TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN remote_id TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN host_gone INTEGER NOT NULL DEFAULT 0;

-- A host's session is added once.
CREATE UNIQUE INDEX sessions_host_remote ON sessions (host, remote_id) WHERE host != '';
