-- 0001: initial schema. Times are Unix milliseconds; 0 = unset.

CREATE TABLE groups (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    color       TEXT NOT NULL DEFAULT '',
    sort_key    INTEGER NOT NULL DEFAULT 0,
    collapsed   INTEGER NOT NULL DEFAULT 0,
    is_default  INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL,
    archived_at INTEGER NOT NULL DEFAULT 0
);

-- Group names are unique (case-insensitive) among non-archived groups.
CREATE UNIQUE INDEX groups_live_name ON groups (lower(name)) WHERE archived_at = 0;

INSERT INTO groups (id, name, color, sort_key, is_default, created_at)
VALUES ('default', 'Default', '', 1000000, 1, 0);

CREATE TABLE sessions (
    id                TEXT PRIMARY KEY,
    name              TEXT NOT NULL DEFAULT '',
    description       TEXT NOT NULL DEFAULT '',
    group_id          TEXT NOT NULL DEFAULT 'default' REFERENCES groups (id),
    name_source       TEXT NOT NULL DEFAULT 'auto',
    cwd               TEXT NOT NULL DEFAULT '',
    claude_session_id TEXT NOT NULL DEFAULT '',
    claude_history    TEXT NOT NULL DEFAULT '[]', -- JSON array of previous Claude session ids
    transcript_path   TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL DEFAULT '',
    status_detail     TEXT NOT NULL DEFAULT '',
    unseen            INTEGER NOT NULL DEFAULT 0,
    kind              TEXT NOT NULL DEFAULT 'background',
    bg_id             TEXT NOT NULL DEFAULT '',
    pid               INTEGER NOT NULL DEFAULT 0,
    external          TEXT NOT NULL DEFAULT '',   -- JSON PaneLoc, '' when not external
    created_at        INTEGER NOT NULL,
    last_event_at     INTEGER NOT NULL DEFAULT 0,
    archived_at       INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX sessions_claude_session_id ON sessions (claude_session_id);
CREATE INDEX sessions_group ON sessions (group_id);

-- Every Claude session id a session has had, current and past, for hook lookup.
CREATE TABLE claude_ids (
    claude_session_id TEXT PRIMARY KEY,
    session_id        TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE
);

CREATE TABLE views (
    id              TEXT PRIMARY KEY,
    pane            TEXT NOT NULL,                -- JSON PaneLoc
    sidebar         TEXT NOT NULL DEFAULT '',     -- JSON PaneLoc
    selected        TEXT NOT NULL DEFAULT '',
    sidebar_hidden  INTEGER NOT NULL DEFAULT 0,
    last_focused_at INTEGER NOT NULL DEFAULT 0
);

-- Single row holding the shared ViewState as JSON.
CREATE TABLE view_state (
    id    INTEGER PRIMARY KEY CHECK (id = 1),
    state TEXT NOT NULL
);

-- Raw event log for doctor/debugging: no prompts,
-- tool inputs or transcript content, only what's needed to explain status.
CREATE TABLE events (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    at                INTEGER NOT NULL,
    session_id        TEXT NOT NULL DEFAULT '',
    claude_session_id TEXT NOT NULL DEFAULT '',
    kind              TEXT NOT NULL,
    detail            TEXT NOT NULL DEFAULT '',
    tool_name         TEXT NOT NULL DEFAULT ''
);
