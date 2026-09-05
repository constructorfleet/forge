-- 0032_feature_planning_leases_owner_token.sql: identify the owning process
-- of a Feature planning lease, not only its pid. The operating system
-- reuses a pid after the process exits, so a bare owner_pid makes a
-- recycled pid look like a live planning lease owner (issue 557). This
-- mirrors migration 0029, which fixed the same defect for Worker claims.
-- owner_token holds a process identity string (the process start time),
-- which changes when the pid is reused. Rows written before this migration
-- keep an empty token, and callers then fall back to a pid liveness test.

ALTER TABLE feature_planning_leases ADD COLUMN owner_token TEXT NOT NULL DEFAULT '';
