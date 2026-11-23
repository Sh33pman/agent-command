-- 0020: harnesses. A harness is one of a kind (claude, antigravity) with its
-- own name and settings, so a kind can be set up more than once (two Claude
-- accounts, each with its own CLAUDE_CONFIG_DIR). Sessions and jobs link to
-- theirs by id. The one Claude and one Antigravity there were become two
-- harnesses named after their kind; Claude's keeps Claude's standard folder
-- (config_dir ''). The daemon moves claude_command, claude_args, agy_command,
-- agy_args and default_harness out of config.toml into them.

CREATE TABLE harnesses (
    id         TEXT PRIMARY KEY,
    kind       TEXT NOT NULL,
    name       TEXT NOT NULL,
    slug       TEXT NOT NULL UNIQUE, -- the name as compared: lower case, "_" for spaces
    position   INTEGER NOT NULL,
    command    TEXT NOT NULL DEFAULT '',
    args       TEXT NOT NULL DEFAULT '[]', -- JSON list
    config_dir TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);

INSERT INTO harnesses (id, kind, name, slug, position, created_at) VALUES
    (upper(hex(randomblob(13))), 'claude', 'Claude', 'claude', 1, CAST(unixepoch('subsec') * 1000 AS INTEGER)),
    (upper(hex(randomblob(13))), 'antigravity', 'Antigravity', 'antigravity', 2, CAST(unixepoch('subsec') * 1000 AS INTEGER));

ALTER TABLE sessions RENAME COLUMN harness TO harness_kind;
ALTER TABLE sessions ADD COLUMN harness_id TEXT NOT NULL DEFAULT '';
UPDATE sessions SET harness_id = (SELECT id FROM harnesses WHERE kind = sessions.harness_kind)
    WHERE harness_kind IN ('claude', 'antigravity');

-- A job's spec named its kind ("harness", "" for claude); it names its harness now.
UPDATE jobs SET spec = json_set(json_remove(spec, '$.harness'), '$.harness_id',
        (SELECT id FROM harnesses WHERE kind = coalesce(nullif(json_extract(jobs.spec, '$.harness'), ''), 'claude')))
    WHERE coalesce(nullif(json_extract(spec, '$.harness'), ''), 'claude') IN ('claude', 'antigravity');
UPDATE jobs SET spec = json_remove(spec, '$.harness');
