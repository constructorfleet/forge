-- 0030_gate_run_agent_run_id.sql: adds agent_run_id to gate_runs (issue
-- 596). Before this column, the TUI found a GateRun's attempt by comparing
-- its finished_at against the Issue's latest AgentRun's started_at — a
-- time-window heuristic. This column is a direct foreign key to the
-- AgentRun a gate run belongs to, so a caller can filter by id instead. A
-- row written before this migration keeps a NULL agent_run_id.

ALTER TABLE gate_runs ADD COLUMN agent_run_id INTEGER REFERENCES agent_runs (id);
