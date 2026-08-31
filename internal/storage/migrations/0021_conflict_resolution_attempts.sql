CREATE TABLE conflict_resolution_attempts (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    execution_id  TEXT NOT NULL,
    issue_id      TEXT NOT NULL,
    pr_number     INTEGER NOT NULL,
    branch        TEXT NOT NULL,
    original_sha  TEXT NOT NULL,
    candidate_sha TEXT NOT NULL,
    status        TEXT NOT NULL,
    details       TEXT NOT NULL,
    created_at    TIMESTAMP NOT NULL,
    updated_at    TIMESTAMP NOT NULL,
    FOREIGN KEY (execution_id, issue_id) REFERENCES execution_issues (execution_id, issue_id)
);

CREATE INDEX idx_conflict_resolution_attempts_issue
    ON conflict_resolution_attempts (execution_id, issue_id, id);
