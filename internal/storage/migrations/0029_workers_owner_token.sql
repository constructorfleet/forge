-- 0029_workers_owner_token.sql: identify the owning process, not only its
-- pid. The operating system reuses a pid after the process exits, so a bare
-- owner_pid makes a recycled pid look like a live Worker owner. owner_token
-- holds a process identity string (the process start time), which changes
-- when the pid is reused. The start time has a granularity of one second on
-- some systems, so a pid reused inside the same second can still give the
-- same token. Rows written before this migration keep an empty token, and
-- callers then fall back to a pid liveness test.

ALTER TABLE workers ADD COLUMN owner_token TEXT NOT NULL DEFAULT '';
