-- 0019: the results of creating ops that carried a request id (design 8.4),
-- so a retry with the same id within 10 minutes gets the first result back
-- instead of starting a second session, job, run or group.

CREATE TABLE requests (
    id         TEXT PRIMARY KEY,
    op         TEXT NOT NULL,
    result     TEXT NOT NULL,
    created_at INTEGER NOT NULL -- unix ms
);
CREATE INDEX requests_created_at ON requests (created_at);
