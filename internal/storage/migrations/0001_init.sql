-- 0001_init.sql: initial Forge storage schema.
--
-- Covers executions, execution_issues (the Issue rows scoped to an
-- Execution), dependencies, workers (claims), workspaces, agent_runs,
-- gate_runs, pull_requests, ci_runs, and the append-only events log. See
-- CONTEXT.md for the domain vocabulary these tables represent and
-- .scratch/forge-mvp/issues/13-sqlite-persistence.md for the acceptance
-- criteria.

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
-- Worker can ever claim a given Issue within an Execution.
CREATE TABLE workers (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    execution_id TEXT NOT NULL,
    issue_id     TEXT NOT NULL,
    worker_ref   TEXT NOT NULL,
    claimed_at   TIMESTAMP NOT NULL,
    UNIQUE (execution_id, issue_id)
);

CREATE TABLE workspaces (
    execution_id TEXT NOT NULL,
    issue_id     TEXT NOT NULL,
    path         TEXT NOT NULL,
    branch       TEXT NOT NULL,
    PRIMARY KEY (execution_id, issue_id)
);

CREATE TABLE agent_runs (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    execution_id TEXT NOT NULL,
    issue_id     TEXT NOT NULL,
    started_at   TIMESTAMP,
    finished_at  TIMESTAMP,
    result       TEXT
);

CREATE TABLE gate_runs (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    execution_id TEXT NOT NULL,
    issue_id     TEXT NOT NULL,
    name         TEXT,
    passed       BOOLEAN,
    ran_at       TIMESTAMP
);

CREATE TABLE pull_requests (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    execution_id TEXT NOT NULL,
    issue_id     TEXT NOT NULL,
    url          TEXT,
    number       INTEGER,
    created_at   TIMESTAMP
);

CREATE TABLE ci_runs (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    execution_id TEXT NOT NULL,
    issue_id     TEXT NOT NULL,
    status       TEXT,
    checked_at   TIMESTAMP
);

-- events is Forge's append-only event log. issue_id is nullable for
-- execution-level events; data holds a JSON payload.
CREATE TABLE events (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    execution_id TEXT NOT NULL,
    issue_id     TEXT,
    type         TEXT NOT NULL,
    data         TEXT,
    occurred_at  TIMESTAMP NOT NULL
);

CREATE INDEX idx_events_execution ON events (execution_id, occurred_at);
CREATE INDEX idx_events_issue ON events (execution_id, issue_id, occurred_at);
