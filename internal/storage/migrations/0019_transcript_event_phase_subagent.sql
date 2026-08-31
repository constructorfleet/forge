-- 0019_transcript_event_phase_subagent.sql: track which workflow phase
-- (IMPLEMENTING, REVIEWING, ...) and which subagent (a review axis such as
-- "bugs"/"quality"/"docs", or empty for the single implementation agent)
-- produced a transcript_events row (issue #219) — needed once the review
-- agent's transcripts start landing in this table alongside the
-- implementation agent's, since both share it and must stay
-- distinguishable per row, not just per agent_run_id.

ALTER TABLE transcript_events ADD COLUMN phase TEXT NOT NULL DEFAULT '';
ALTER TABLE transcript_events ADD COLUMN subagent TEXT NOT NULL DEFAULT '';
