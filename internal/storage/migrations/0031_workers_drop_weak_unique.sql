-- 0031_workers_drop_weak_unique.sql: drops the weaker
-- UNIQUE(execution_id, issue_id) constraint on workers (issue #564).
--
-- 0011_global_issue_claims.sql added idx_workers_issue_claim_unique, a
-- UNIQUE index on issue_id alone. That index is strictly stronger: one
-- active claim per Issue across all Executions implies one claim per
-- (execution_id, issue_id) pair. The table-level UNIQUE from 0001_init.sql
-- is now dead weight and misleads a reader about what activeClaimByIssue's
-- WHERE issue_id = ? actually guarantees. SQLite cannot drop a table-level
-- UNIQUE constraint with ALTER TABLE, hence the create-copy-drop-rename
-- rebuild.

CREATE TABLE workers_new (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    execution_id   TEXT NOT NULL,
    issue_id       TEXT NOT NULL,
    worker_ref     TEXT NOT NULL,
    claimed_at     TIMESTAMP NOT NULL,
    owner_pid      INTEGER NOT NULL DEFAULT 0,
    last_heartbeat TIMESTAMP,
    owner_token    TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (execution_id, issue_id) REFERENCES execution_issues (execution_id, issue_id)
);
INSERT INTO workers_new
    (id, execution_id, issue_id, worker_ref, claimed_at, owner_pid, last_heartbeat, owner_token)
SELECT id, execution_id, issue_id, worker_ref, claimed_at, owner_pid, last_heartbeat, owner_token
FROM workers;

DROP TABLE workers;

ALTER TABLE workers_new RENAME TO workers;

CREATE UNIQUE INDEX idx_workers_issue_claim_unique ON workers (issue_id);
