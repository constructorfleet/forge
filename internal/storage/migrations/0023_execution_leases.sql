-- 0023_execution_leases.sql: the remote-execution lease and placement
-- record (ticket 341, parent #287 workstream C).
--
-- execution_leases follows feature_planning_leases' pattern (existence of a
-- row IS the active lease, so a duplicate claim is a constraint violation
-- rather than a read-then-write race), keyed by execution_id/issue_id the
-- same way workers is. Unlike a planning lease, an execution lease adds a
-- heartbeat and an expiry: the worker heartbeats while it works, and a
-- lapsed expiry is how a later ticket detects a lost worker. This ticket
-- adds the storage and the heartbeat mechanism only, not loss detection.
CREATE TABLE execution_leases (
    execution_id TEXT NOT NULL,
    issue_id     TEXT NOT NULL,
    heartbeat_at TIMESTAMP NOT NULL,
    expires_at   TIMESTAMP NOT NULL,
    claimed_at   TIMESTAMP NOT NULL,
    PRIMARY KEY (execution_id, issue_id),
    FOREIGN KEY (execution_id, issue_id) REFERENCES execution_issues (execution_id, issue_id)
);

-- execution_placements records the remote-execution substrate facts for one
-- Issue execution: the backend and worker that ran it, the Workspace the
-- worker prepared, and the current workspace-lifecycle state (ACTIVE or
-- LOST). This is separate from execution_leases, the claim/heartbeat
-- mechanism, and separate from Issue state (internal/domain/state.go):
-- LOST is a workspace-lifecycle state, not an IssueState (see ADR 0020).
CREATE TABLE execution_placements (
    execution_id     TEXT NOT NULL,
    issue_id         TEXT NOT NULL,
    backend          TEXT NOT NULL,
    worker_ref       TEXT NOT NULL,
    workspace_path   TEXT NOT NULL,
    workspace_branch TEXT NOT NULL,
    lifecycle        TEXT NOT NULL,
    PRIMARY KEY (execution_id, issue_id),
    FOREIGN KEY (execution_id, issue_id) REFERENCES execution_issues (execution_id, issue_id)
);
