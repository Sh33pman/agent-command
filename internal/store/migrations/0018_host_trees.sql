-- 0018: a host's sessions show here as the host has them, in its own groups
-- and sets (design 6.14). The ones added here one at a time, into this
-- machine's groups, go; their host keeps them.

DELETE FROM inbox_items WHERE session_id IN (SELECT id FROM sessions WHERE host != '');
DELETE FROM claude_ids WHERE session_id IN (SELECT id FROM sessions WHERE host != '');
DELETE FROM events WHERE session_id IN (SELECT id FROM sessions WHERE host != '');
DELETE FROM sets WHERE id IN (SELECT set_id FROM sessions WHERE host != '' AND set_id != '');
UPDATE sessions SET set_id = '', set_order = 0 WHERE set_id != '' AND set_id NOT IN (SELECT id FROM sets);
DELETE FROM sessions WHERE host != '';
