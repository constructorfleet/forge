-- 0012_planning_executions.sql: the Planning runtime container (ticket 11).
--
-- planning_executions mirrors executions (id, base_revision, started_at)
-- but is a distinct table rather than a shared one: Planning Executions
-- carry a stored runtime Status (ACTIVE/NEEDS_HUMAN/NEEDS_APPROVAL/FAILED/
-- COMPLETE) that coding Executions have no equivalent of, and mixing the
-- two would leak planning-only columns into ticket-execution code paths.
CREATE TABLE planning_executions (
    id            TEXT PRIMARY KEY,
    feature_id    TEXT NOT NULL,
    base_revision TEXT NOT NULL,
    status        TEXT NOT NULL,
    started_at    TIMESTAMP NOT NULL
);

CREATE INDEX idx_planning_executions_feature ON planning_executions (feature_id);

-- feature_planning_leases enforces "at most one active Planning Execution
-- per Feature" the same way workers.idx_workers_issue_claim_unique enforces
-- one active Worker claim per Issue: existence of a row IS the active
-- claim, so the primary key on feature_id makes double-claiming a
-- constraint violation rather than a read-then-write race. owner_pid
-- supports the same PID-liveness abandoned-lease recovery
-- ensureRecoverableWorker implements for Worker claims.
CREATE TABLE feature_planning_leases (
    feature_id   TEXT PRIMARY KEY,
    execution_id TEXT NOT NULL REFERENCES planning_executions (id),
    owner_pid    INTEGER NOT NULL,
    claimed_at   TIMESTAMP NOT NULL
);
