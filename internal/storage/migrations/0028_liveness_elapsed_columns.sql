-- 0028_liveness_elapsed_columns.sql: display-only liveness and
-- elapsed-in-state columns for the TUI (issue #494).
--
-- workers.last_heartbeat advances on each Worker heartbeat; the TUI
-- renders liveness from it. execution_issues.state_changed_at records
-- the moment an Issue last changed state, written in the transition
-- transaction. Both are display-only: no state machine or loss-detection
-- logic reads them. Existing rows get NULL (unknown until next write).

ALTER TABLE workers
ADD COLUMN last_heartbeat TIMESTAMP;

ALTER TABLE execution_issues
ADD COLUMN state_changed_at TIMESTAMP;
