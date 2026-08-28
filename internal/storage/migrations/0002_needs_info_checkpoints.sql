-- 0002_needs_info_checkpoints.sql: persists the needs-info checkpoint an
-- Issue records when it transitions to NEEDS_INFO (ticket 28, issue 07's
-- needs-info resume flow) — the question asked, why it was asked, when,
-- and whether the label/comment side effects have already run, so
-- `forge resume` can detect new human input and the NEEDS_INFO handling
-- itself stays idempotent across repeats.
CREATE TABLE needs_info_checkpoints (
    execution_id     TEXT NOT NULL,
    issue_id         TEXT NOT NULL,
    question         TEXT NOT NULL,
    reason           TEXT,
    label_added      BOOLEAN NOT NULL,
    comment_posted   BOOLEAN NOT NULL,
    created_at       TIMESTAMP NOT NULL,
    resumed_at       TIMESTAMP,
    resumed_context  TEXT,
    PRIMARY KEY (execution_id, issue_id),
    FOREIGN KEY (execution_id, issue_id) REFERENCES execution_issues (execution_id, issue_id)
);
