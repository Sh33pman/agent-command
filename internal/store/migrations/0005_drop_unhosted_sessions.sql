-- Sessions v1 tracked but didn't start: Claude keeps their conversations, which can
-- still be resumed. The app only lists sessions it runs.
DELETE FROM claude_ids WHERE session_id IN (SELECT id FROM sessions WHERE kind <> 'hosted');
DELETE FROM events WHERE session_id IN (SELECT id FROM sessions WHERE kind <> 'hosted');
DELETE FROM sessions WHERE kind <> 'hosted';
