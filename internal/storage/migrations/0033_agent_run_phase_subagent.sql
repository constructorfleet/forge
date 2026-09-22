-- 0033_agent_run_phase_subagent.sql: identify review runs before events exist.
ALTER TABLE agent_runs ADD COLUMN phase TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_runs ADD COLUMN subagent TEXT NOT NULL DEFAULT '';
