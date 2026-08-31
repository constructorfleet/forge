-- 0017_review_axis_envelopes.sql: extends review_runs (issue #162, full
-- per-axis review audit trail) with review_axis_envelopes, one row per axis
-- (bugs/quality/docs) attempted during a Review call: whether it ran to
-- completion (mirroring review.AxisCoverage) and why not when it didn't,
-- its per-axis token usage, and its raw findings envelope as JSON. The raw
-- envelope is kept as an opaque JSON blob rather than individually
-- queryable columns like review_findings, since this is audit/
-- reconstruction detail rather than data any query needs to filter on
-- directly (mirroring how gate_runs keeps stdout/stderr as plain text).
CREATE TABLE review_axis_envelopes (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    review_run_id INTEGER NOT NULL REFERENCES review_runs (id),
    axis          TEXT NOT NULL,
    ran           BOOLEAN NOT NULL,
    reason        TEXT NOT NULL DEFAULT '',
    input_tokens  INTEGER,
    output_tokens INTEGER,
    raw_findings  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_review_axis_envelopes_run ON review_axis_envelopes (review_run_id);
