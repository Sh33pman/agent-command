-- Every session is hosted: a Claude acmd didn't start makes none, and
-- `claude --bg` sessions and the user's own panes are no longer tracked.
DELETE FROM claude_ids WHERE session_id IN (SELECT id FROM sessions WHERE kind <> 'hosted');
DELETE FROM events WHERE session_id IN (SELECT id FROM sessions WHERE kind <> 'hosted');
DELETE FROM sessions WHERE kind <> 'hosted';
ALTER TABLE sessions DROP COLUMN bg_id;
ALTER TABLE sessions DROP COLUMN external;
