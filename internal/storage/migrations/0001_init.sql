-- 0001_init.sql: initial Forge storage schema.
--
-- Covers executions, execution_issues (the Issue rows scoped to an
-- Execution), dependencies, workers (claims), workspaces, agent_runs,
-- gate_runs, pull_requests, ci_runs, and the append-only events log. See
-- CONTEXT.md for the domain vocabulary these tables represent and
-- .scratch/forge-mvp/issues/13-sqlite-persistence.md for the acceptance
-- criteria.
--
-- workspaces/agent_runs/gate_runs/pull_requests/ci_runs are schema-forward:
-- no ticket before this one writes to them, but they are held to the same
-- rigor as the live tables (FKs to execution_issues, NOT NULL on columns
-- that are logically required, and lookup indexes) so later tickets
-- (15, 19, 20, 22, 23) can build on a sound foundation rather than retrofit
-- one.

CREATE TABLE executions (
    id            TEXT PRIMARY KEY,
    base_revision TEXT NOT NULL,
    started_at    TIMESTAMP NOT NULL
);

-- execution_issues is Forge's persisted Issue: one row per Issue within an
-- Execution, including its current state, scope, and retry-budget counters.
CREATE TABLE execution_issues (
    execution_id       TEXT NOT NULL REFERENCES executions (id),
    issue_id           TEXT NOT NULL,
    state              TEXT NOT NULL,
    scope              TEXT NOT NULL,
    retry_gate_limit   INTEGER NOT NULL,
    retry_gate_used    INTEGER NOT NULL,
    retry_review_limit INTEGER NOT NULL,
    retry_review_used  INTEGER NOT NULL,
    retry_ci_limit     INTEGER NOT NULL,
    retry_ci_used      INTEGER NOT NULL,
    PRIMARY KEY (execution_id, issue_id)
);

-- dependencies is a directed edge issue_id -> depends_on_id. A Dependency is
-- satisfied only when depends_on_id's PR merges (CONTEXT.md "Dependency"),
-- not tracked here — this table only records the DAG shape.
CREATE TABLE dependencies (
    execution_id  TEXT NOT NULL,
    issue_id      TEXT NOT NULL,
    depends_on_id TEXT NOT NULL,
    PRIMARY KEY (execution_id, issue_id, depends_on_id),
    FOREIGN KEY (execution_id, issue_id) REFERENCES execution_issues (execution_id, issue_id)
);

-- workers records Issue claims. The UNIQUE constraint on
-- (execution_id, issue_id) is the database-level guarantee that only one
-- Worker can ever claim a given Issue within an Execution. The FK ensures a
-- claim can never be written for an Issue that doesn't exist.
CREATE TABLE workers (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    execution_id TEXT NOT NULL,
    issue_id     TEXT NOT NULL,
    worker_ref   TEXT NOT NULL,
    claimed_at   TIMESTAMP NOT NULL,
    UNIQUE (execution_id, issue_id),
    FOREIGN KEY (execution_id, issue_id) REFERENCES execution_issues (execution_id, issue_id)
);

CREATE TABLE workspaces (
    execution_id TEXT NOT NULL,
    issue_id     TEXT NOT NULL,
    path         TEXT NOT NULL,
    branch       TEXT NOT NULL,
    PRIMARY KEY (execution_id, issue_id),
    FOREIGN KEY (execution_id, issue_id) REFERENCES execution_issues (execution_id, issue_id)
);

CREATE TABLE agent_runs (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    execution_id TEXT NOT NULL,
    issue_id     TEXT NOT NULL,
    started_at   TIMESTAMP NOT NULL,
    finished_at  TIMESTAMP,
    result       TEXT,
    FOREIGN KEY (execution_id, issue_id) REFERENCES execution_issues (execution_id, issue_id)
);
CREATE INDEX idx_agent_runs_issue ON agent_runs (execution_id, issue_id);

CREATE TABLE gate_runs (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    execution_id TEXT NOT NULL,
    issue_id     TEXT NOT NULL,
    name         TEXT NOT NULL,
    passed       BOOLEAN NOT NULL,
    ran_at       TIMESTAMP NOT NULL,
    FOREIGN KEY (execution_id, issue_id) REFERENCES execution_issues (execution_id, issue_id)
);
CREATE INDEX idx_gate_runs_issue ON gate_runs (execution_id, issue_id);

CREATE TABLE pull_requests (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    execution_id TEXT NOT NULL,
    issue_id     TEXT NOT NULL,
    url          TEXT NOT NULL,
    number       INTEGER NOT NULL,
    created_at   TIMESTAMP NOT NULL,
    FOREIGN KEY (execution_id, issue_id) REFERENCES execution_issues (execution_id, issue_id)
);
CREATE INDEX idx_pull_requests_issue ON pull_requests (execution_id, issue_id);

CREATE TABLE ci_runs (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    execution_id TEXT NOT NULL,
    issue_id     TEXT NOT NULL,
    status       TEXT NOT NULL,
    checked_at   TIMESTAMP NOT NULL,
    FOREIGN KEY (execution_id, issue_id) REFERENCES execution_issues (execution_id, issue_id)
);
CREATE INDEX idx_ci_runs_issue ON ci_runs (execution_id, issue_id);

-- events is Forge's append-only event log. issue_id is nullable for
-- execution-level events; data holds a JSON payload.
CREATE TABLE events (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    execution_id TEXT NOT NULL,
    issue_id     TEXT,
    type         TEXT NOT NULL,
    data         TEXT,
    occurred_at  TIMESTAMP NOT NULL,
    FOREIGN KEY (execution_id) REFERENCES executions (id)
);

CREATE INDEX idx_events_execution ON events (execution_id, occurred_at);
CREATE INDEX idx_events_issue ON events (execution_id, issue_id, occurred_at);
