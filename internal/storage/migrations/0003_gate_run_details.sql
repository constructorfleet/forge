-- 0002_gate_run_details.sql: extends gate_runs (ticket 19, the Quality
-- Gate Runner) with the fields it actually needs to persist per CONTEXT.md
-- "Gate Runner": the command that ran, its execution window, exit code,
-- and captured (bounded) stdout/stderr. 0001_init.sql's gate_runs was
-- schema-forward with only name/passed/ran_at; this fills in the rest
-- rather than widening 0001 after the fact, since already-applied
-- migrations are immutable.
--
-- ran_at (0001) is kept as-is and continues to mean "when this gate run
-- was recorded" (finished_at); started_at is added alongside it so a
-- gate's execution window is fully recoverable.

ALTER TABLE gate_runs ADD COLUMN command TEXT NOT NULL DEFAULT '';
ALTER TABLE gate_runs ADD COLUMN started_at TIMESTAMP;
ALTER TABLE gate_runs ADD COLUMN exit_code INTEGER NOT NULL DEFAULT 0;
ALTER TABLE gate_runs ADD COLUMN stdout TEXT NOT NULL DEFAULT '';
ALTER TABLE gate_runs ADD COLUMN stderr TEXT NOT NULL DEFAULT '';
