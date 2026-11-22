-- 0015: what each client keeps in the daemon: its notification profile, the
-- warnings it dismissed, and its keymap (each JSON, '' for none).

ALTER TABLE clients ADD COLUMN profile TEXT NOT NULL DEFAULT '';
ALTER TABLE clients ADD COLUMN dismissed TEXT NOT NULL DEFAULT '';
ALTER TABLE clients ADD COLUMN keymap TEXT NOT NULL DEFAULT '';

-- Each client's dismissed warnings move here from where 1.3 kept them.
INSERT INTO clients (id, first_seen, last_seen, dismissed)
    SELECT substr(key, 18), CAST(strftime('%s', 'now') AS INTEGER) * 1000, CAST(strftime('%s', 'now') AS INTEGER) * 1000, value
    FROM app_state WHERE key LIKE 'health.dismissed:%' AND value != '' AND true
    ON CONFLICT (id) DO UPDATE SET dismissed = excluded.dismissed;
DELETE FROM app_state WHERE key LIKE 'health.dismissed:%';
