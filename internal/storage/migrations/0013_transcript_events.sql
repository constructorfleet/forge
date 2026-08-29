-- 0013_transcript_events.sql: per-attempt Agent transcript capture (ticket
-- 28). One row per observed message/tool-call/tool-result, keyed to the
-- agent_runs row (one attempt) it was captured during, so a run's full
-- transcript is recoverable after the process exits.

CREATE TABLE transcript_events (
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
    FOREIGN KEY (execution_id, issue_id) REFERENCES execution_issues (execution_id, issue_id)
);
CREATE INDEX idx_transcript_events_run ON transcript_events (agent_run_id, seq);
CREATE INDEX idx_transcript_events_issue ON transcript_events (execution_id, issue_id, agent_run_id, seq);
