-- 0027_transcript_events_unique_seq.sql: UNIQUE (agent_run_id, seq) for a
-- stable per-run arrival ordinal (issue #489 / ADR 0030).
--
-- `seq` becomes a stable ordinal assigned once at Emit and never renumbered,
-- so a double-write of the same ordinal must fail loudly rather than append a
-- silent duplicate. SQLite cannot add a UNIQUE column constraint with ALTER
-- TABLE, hence the create-copy-drop-rename rebuild. Existing rows carry a
-- dense 0-based seq with no duplicates per run, so they copy in unchanged --
-- no backfill is needed.

CREATE TABLE transcript_events_new (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    execution_id TEXT NOT NULL,
    issue_id     TEXT NOT NULL,
    agent_run_id INTEGER NOT NULL REFERENCES agent_runs (id),
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
    subagent     TEXT NOT NULL DEFAULT '',
    UNIQUE (agent_run_id, seq)
);
INSERT INTO transcript_events_new
    (id, execution_id, issue_id, agent_run_id, seq, type, role, text, tool_name, tool_input, tool_output, occurred_at, tool_call_id, phase, subagent)
SELECT id, execution_id, issue_id, agent_run_id, seq, type, role, text, tool_name, tool_input, tool_output, occurred_at, tool_call_id, phase, subagent
FROM transcript_events;

DROP TABLE transcript_events;

ALTER TABLE transcript_events_new RENAME TO transcript_events;

CREATE INDEX idx_transcript_events_run ON transcript_events (agent_run_id, seq);
CREATE INDEX idx_transcript_events_issue ON transcript_events (execution_id, issue_id, agent_run_id, seq);
