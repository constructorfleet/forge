-- 0007_ci_run_details.sql: extends ci_runs (ticket 23, CI supervisor) with
-- the specific check that failed, bounded diagnostics, and preserves the
-- existing checked_at timestamp from 0001_init.sql. 0001's ci_runs table was
-- schema-forward with only status/checked_at; this migration fills in the
-- details now that the table is live.

ALTER TABLE ci_runs ADD COLUMN check_name TEXT NOT NULL DEFAULT '';
ALTER TABLE ci_runs ADD COLUMN details TEXT NOT NULL DEFAULT '';
