-- 0014_decision_checkpoints.sql: persists the checkpoint a Planning
-- Execution records when a wayfinding Decision resolves to NEEDS_HUMAN
-- (ticket 15a) -- the question asked, the Decision provenance it arose
-- from, when, and whether the label/comment side effects have already run
-- -- mirroring needs_info_checkpoints' idempotency guard so the NEEDS_HUMAN
-- handling in internal/wayfinding stays idempotent across repeats and a
-- future `forge resume` (ticket 15b) can detect a human response newer
-- than the checkpoint.
--
-- decision_revision records the Decision Artifact's content revision at
-- the moment it paused -- its provenance -- so a checkpoint stays
-- attributable to the exact Decision content that raised the question even
-- if the Decision is later re-resolved under a new revision.
CREATE TABLE decision_checkpoints (
    execution_id      TEXT NOT NULL,
    decision_id       TEXT NOT NULL,
    decision_revision TEXT NOT NULL,
    question          TEXT NOT NULL,
    context           TEXT,
    label_added       BOOLEAN NOT NULL,
    comment_posted    BOOLEAN NOT NULL,
    comment_author    TEXT,
    comment_posted_at TIMESTAMP,
    created_at        TIMESTAMP NOT NULL,
    resumed_at        TIMESTAMP,
    resumed_context   TEXT,
    PRIMARY KEY (execution_id, decision_id),
    FOREIGN KEY (execution_id) REFERENCES planning_executions (id)
);