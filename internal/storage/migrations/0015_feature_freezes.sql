-- 0015_feature_freezes.sql: persists a Feature's replan freeze (ticket 22,
-- conservative replanning). A frozen Feature schedules no new work and
-- integrates no in-flight work: Workers already running may finish to their
-- safe suspension boundary, but the transition that would integrate against
-- the invalidated plan is refused while a row exists here.
--
-- feature_id is the primary key, so freezing is naturally idempotent (a
-- second REPLAN_REQUIRED escalation for the same Feature refreshes the
-- reason rather than stacking freezes) and "is this Feature frozen" is a
-- single-row lookup. The freeze is deliberately its own table, not a column
-- on feature_planning_leases: it must be writable *before* a planning lease
-- exists, since the freeze is what makes acquiring that lease safe.
CREATE TABLE feature_freezes (
    feature_id          TEXT PRIMARY KEY,
    reason              TEXT NOT NULL,
    triggering_issue_id TEXT NOT NULL,
    created_at          TIMESTAMP NOT NULL
);

-- 0015: replan_checkpoints persists the structural escalation itself --
-- what the reporting Worker discovered, under which ticket plan revision,
-- and which of the freeze / lease / decision side effects have already run
-- -- so handleReplanRequired stays idempotent across repeats, exactly as
-- needs_info_checkpoints does for NEEDS_INFO.
CREATE TABLE replan_checkpoints (
    execution_id          TEXT NOT NULL,
    issue_id              TEXT NOT NULL,
    feature_id            TEXT NOT NULL,
    reason                TEXT NOT NULL,
    evidence              TEXT,
    affected_requirements TEXT,
    suggested_question    TEXT,
    plan_revision         TEXT,
    decision_id           TEXT,
    frozen                BOOLEAN NOT NULL,
    lease_execution_id    TEXT,
    label_added           BOOLEAN NOT NULL,
    comment_posted        BOOLEAN NOT NULL,
    comment_author        TEXT,
    comment_posted_at     TIMESTAMP,
    created_at            TIMESTAMP NOT NULL,
    PRIMARY KEY (execution_id, issue_id)
);
