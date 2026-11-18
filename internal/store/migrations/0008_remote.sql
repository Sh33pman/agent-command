-- 0008: Remote sessions. Clients of this acmd's Hosting may see and use a
-- session marked remote. Sessions from before stay local.

ALTER TABLE sessions ADD COLUMN remote INTEGER NOT NULL DEFAULT 0;
