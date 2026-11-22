-- 0014: the client registry: the apps, terminal UIs and other clients that
-- use this daemon, by the id each one keeps. Presence is how the person is
-- using it; last_active when they last were.

CREATE TABLE clients (
    id          TEXT PRIMARY KEY,
    kind        TEXT NOT NULL DEFAULT '',
    device      TEXT NOT NULL DEFAULT '',
    caps        TEXT NOT NULL DEFAULT '',
    on_host     INTEGER NOT NULL DEFAULT 0,
    presence    TEXT NOT NULL DEFAULT '',
    focus       TEXT NOT NULL DEFAULT '',
    first_seen  INTEGER NOT NULL,
    last_seen   INTEGER NOT NULL,
    last_active INTEGER NOT NULL DEFAULT 0
);
