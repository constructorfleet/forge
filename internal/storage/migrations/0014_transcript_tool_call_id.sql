-- 0014_transcript_tool_call_id.sql: add the tool-call id that pairs a
-- TOOL_RESULT row back to the TOOL_CALL row that produced it (issue 36).
-- Ticket 28 stored the referenced id in tool_name, conflating it with the
-- human-readable tool name; a dedicated column lets a reader join a result
-- to its call without orphans while tool_name stays the tool's real name.

ALTER TABLE transcript_events ADD COLUMN tool_call_id TEXT NOT NULL DEFAULT '';
