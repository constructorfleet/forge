-- review_overrides persists a non-convergent review finding (issue #375):
-- a finding that repeats identically across review retries against
-- unchanged code. It is keyed by issue_id alone, not (execution_id,
-- issue_id), so the override survives into a new Execution for the same
-- Issue/branch rather than being scoped to the Execution that detected it.
CREATE TABLE review_overrides (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    issue_id   TEXT NOT NULL,
    signature  TEXT NOT NULL,
    axis       TEXT NOT NULL,
    file       TEXT NOT NULL,
    line       INTEGER NOT NULL,
    message    TEXT NOT NULL,
    reason     TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL,
    UNIQUE (issue_id, signature)
);

CREATE INDEX idx_review_overrides_issue ON review_overrides (issue_id);
