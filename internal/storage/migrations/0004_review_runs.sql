-- 0004_review_runs.sql: persists Review invocations (ticket 20, CONTEXT.md
-- "Review") and their structured Findings, following the same pattern
-- 0001_init.sql/0002_gate_run_details.sql established for gate_runs rather
-- than widening the schema-forward agent_runs table (agent_runs models a
-- generic Agent invocation's start/finish/result and has no room for
-- Review's verdict/findings shape without overloading its columns).
--
-- review_findings is a child table (one row per Finding) rather than a
-- single JSON blob column, so severity/file/line/message stay individually
-- queryable — matching how gate_runs keeps stdout/stderr as plain columns
-- rather than an opaque payload.

CREATE TABLE review_runs (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    execution_id TEXT NOT NULL,
    issue_id     TEXT NOT NULL,
    verdict      TEXT NOT NULL,
    summary      TEXT NOT NULL DEFAULT '',
    diff         TEXT NOT NULL DEFAULT '',
    started_at   TIMESTAMP NOT NULL,
    finished_at  TIMESTAMP NOT NULL,
    FOREIGN KEY (execution_id, issue_id) REFERENCES execution_issues (execution_id, issue_id)
);
CREATE INDEX idx_review_runs_issue ON review_runs (execution_id, issue_id);

CREATE TABLE review_findings (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    review_run_id INTEGER NOT NULL REFERENCES review_runs (id),
    severity      TEXT NOT NULL,
    file          TEXT NOT NULL DEFAULT '',
    line          INTEGER NOT NULL DEFAULT 0,
    message       TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_review_findings_run ON review_findings (review_run_id);
