-- 0011: sets, the sessions made together by splitting panes. A set has at
-- least two live sessions, all in one group and on one host; a session is in
-- one set at most. set_order is a member's place in its set, from 1.

CREATE TABLE sets (
    id         TEXT PRIMARY KEY,
    created_at INTEGER NOT NULL
);

ALTER TABLE sessions ADD COLUMN set_id TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN set_order INTEGER NOT NULL DEFAULT 0;

CREATE INDEX sessions_set ON sessions (set_id) WHERE set_id != '';
