-- 0016: who made each change, in the event log.

ALTER TABLE events ADD COLUMN caller TEXT NOT NULL DEFAULT '';
