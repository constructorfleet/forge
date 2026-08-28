-- 0005_issue_title.sql: adds the Issue's Title to execution_issues (ticket
-- 22, commit/PR creation). Title is needed once Execute reaches COMMITTING:
-- the default commit-message and pull-request-title templates render it,
-- and Store.TransitionIssue always reloads the Issue fresh from this table
-- (see execution_issues' PRIMARY KEY comment in 0001_init.sql) — without a
-- persisted column, Title would be silently dropped from the in-memory
-- Issue by the very first transition after GetIssue populates it.
--
-- Widening execution_issues rather than adding a child table matches
-- 0003_gate_run_details.sql's precedent: Title is a single scalar owned
-- one-to-one with the Issue row, not a repeating/child concept.

ALTER TABLE execution_issues ADD COLUMN title TEXT NOT NULL DEFAULT '';
