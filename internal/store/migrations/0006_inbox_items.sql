-- What sessions have sent you: finished turns, questions, permissions and errors.
-- The id gives arrival order, which is the queue's order.
CREATE TABLE inbox_items (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT    NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    kind       TEXT    NOT NULL,
    text       TEXT    NOT NULL,
    options    TEXT    NOT NULL DEFAULT '[]',
    multipart  INTEGER NOT NULL DEFAULT 0,
    at         INTEGER NOT NULL,
    read       INTEGER NOT NULL DEFAULT 0,
    state      TEXT    NOT NULL DEFAULT 'open',
    handled_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX inbox_items_state ON inbox_items (state, id);
CREATE INDEX inbox_items_session ON inbox_items (session_id, id);
