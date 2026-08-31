-- 0020_planning_scoped_agent_runs.sql: let agent_runs, transcript_events,
-- and events hold Planning-phase rows (issue #248).
--
-- Every agent invocation's transcript must land in transcript_events as it
-- happens, including `forge plan`'s planning agents (wayfinding, spec
-- generation/review, ticket-plan generation/review). Those run against
-- planning_executions (0012), a deliberately separate table from
-- executions/execution_issues -- there is no execution_issues row for a
-- Feature being planned. The 0001 foreign keys
--   agent_runs(execution_id, issue_id)       -> execution_issues
--   transcript_events(execution_id, issue_id)-> execution_issues
--   events(execution_id)                     -> executions
-- therefore made planning-scoped rows unwritable: with foreign_keys on (see
-- storage.withPragmas) every insert failed the constraint. These three
-- tables are the shared audit surface for *any* agent invocation, not just
-- ticket execution, so the constraint is dropped rather than duplicated
-- into planning-only twins.
--
-- Only these three constraints go away. execution_issues/executions keep
-- their own FKs, every other table keeps its FK to them (Phase-1 integrity
-- is unaffected), and transcript_events.agent_run_id -> agent_runs(id)
-- stays: planning agent_runs rows live in agent_runs too, so that one is
-- still meaningful and still rejects orphan transcripts.
--
-- SQLite cannot drop a FOREIGN KEY with ALTER TABLE, hence the usual
-- create-copy-drop-rename dance. Two wrinkles, both handled by ordering
-- rather than by PRAGMA foreign_keys (migrate.go runs each migration file
-- inside one transaction, and that pragma is a no-op inside a transaction):
--   * DROP TABLE performs an implicit DELETE that is FK-checked, so
--     transcript_events is pointed at agent_runs_new *before* the old
--     agent_runs is dropped, leaving no child rows referencing it.
--   * ALTER TABLE ... RENAME rewrites REFERENCES clauses in other tables,
--     so renaming agent_runs_new -> agent_runs repoints
--     transcript_events_new's FK at the final name automatically.
-- Copying rows into strictly-less-strict tables cannot violate anything, so
-- no data can be lost or rejected here.

CREATE TABLE agent_runs_new (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    execution_id  TEXT NOT NULL,
    issue_id      TEXT NOT NULL,
    started_at    TIMESTAMP NOT NULL,
    finished_at   TIMESTAMP,
    result        TEXT,
    backend       TEXT NOT NULL DEFAULT '',
    context_bytes INTEGER NOT NULL DEFAULT 0,
    input_tokens  INTEGER,
    output_tokens INTEGER
);
INSERT INTO agent_runs_new
    (id, execution_id, issue_id, started_at, finished_at, result, backend, context_bytes, input_tokens, output_tokens)
SELECT id, execution_id, issue_id, started_at, finished_at, result, backend, context_bytes, input_tokens, output_tokens
FROM agent_runs;

CREATE TABLE transcript_events_new (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    execution_id TEXT NOT NULL,
    issue_id     TEXT NOT NULL,
    agent_run_id INTEGER NOT NULL REFERENCES agent_runs_new (id),
    seq          INTEGER NOT NULL,
    type         TEXT NOT NULL,
    role         TEXT NOT NULL DEFAULT '',
    text         TEXT NOT NULL DEFAULT '',
    tool_name    TEXT NOT NULL DEFAULT '',
    tool_input   TEXT NOT NULL DEFAULT '',
    tool_output  TEXT NOT NULL DEFAULT '',
    occurred_at  TIMESTAMP NOT NULL,
    tool_call_id TEXT NOT NULL DEFAULT '',
    phase        TEXT NOT NULL DEFAULT '',
    subagent     TEXT NOT NULL DEFAULT ''
);
INSERT INTO transcript_events_new
    (id, execution_id, issue_id, agent_run_id, seq, type, role, text, tool_name, tool_input, tool_output, occurred_at, tool_call_id, phase, subagent)
SELECT id, execution_id, issue_id, agent_run_id, seq, type, role, text, tool_name, tool_input, tool_output, occurred_at, tool_call_id, phase, subagent
FROM transcript_events;

CREATE TABLE events_new (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    execution_id TEXT NOT NULL,
    issue_id     TEXT,
    type         TEXT NOT NULL,
    data         TEXT,
    occurred_at  TIMESTAMP NOT NULL
);
INSERT INTO events_new (id, execution_id, issue_id, type, data, occurred_at)
SELECT id, execution_id, issue_id, type, data, occurred_at FROM events;

DROP TABLE transcript_events;
DROP TABLE agent_runs;
DROP TABLE events;

ALTER TABLE agent_runs_new RENAME TO agent_runs;
ALTER TABLE transcript_events_new RENAME TO transcript_events;
ALTER TABLE events_new RENAME TO events;

CREATE INDEX idx_agent_runs_issue ON agent_runs (execution_id, issue_id);
CREATE INDEX idx_transcript_events_run ON transcript_events (agent_run_id, seq);
CREATE INDEX idx_transcript_events_issue ON transcript_events (execution_id, issue_id, agent_run_id, seq);
CREATE INDEX idx_events_execution ON events (execution_id, occurred_at);
CREATE INDEX idx_events_issue ON events (execution_id, issue_id, occurred_at);
