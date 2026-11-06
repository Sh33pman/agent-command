-- 0003: small values the app keeps, such as its pane layout.

CREATE TABLE app_state (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
