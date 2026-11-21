-- 0013: what Remote keeps that changes through ops, out of config.toml.
-- remote_clients are the clients Hosting lets in; remote_hosts the hosts
-- this machine connects to. Each is listed in the order it was added.
-- Entries a person writes in config.toml still count, read-only.

CREATE TABLE remote_clients (
    fingerprint TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    added_at    INTEGER NOT NULL
);

CREATE TABLE remote_hosts (
    name        TEXT PRIMARY KEY,
    address     TEXT NOT NULL,
    fingerprint TEXT NOT NULL DEFAULT '',
    cert        TEXT NOT NULL DEFAULT '',
    added_at    INTEGER NOT NULL
);
