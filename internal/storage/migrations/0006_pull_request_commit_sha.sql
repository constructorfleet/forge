-- 0006_pull_request_commit_sha.sql: extends pull_requests (ticket 22,
-- commit/PR creation) with the commit SHA the Publisher committed and
-- pushed before the pull request was created — 0001_init.sql's
-- pull_requests was schema-forward with only url/number/created_at, which
-- this ticket is the first to write to; commit_sha is added rather than
-- widening 0001 after the fact, since already-applied migrations are
-- immutable (see 0003_gate_run_details.sql for the same precedent).

ALTER TABLE pull_requests ADD COLUMN commit_sha TEXT NOT NULL DEFAULT '';
