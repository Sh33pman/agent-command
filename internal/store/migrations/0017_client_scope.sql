-- 0017: what each authorized client may do on this host: read, act or admin
-- ('' is act, what a newly approved client gets). Clients authorized before
-- scopes keep everything they could do: admin.

ALTER TABLE remote_clients ADD COLUMN scope TEXT NOT NULL DEFAULT '';
UPDATE remote_clients SET scope = 'admin';
