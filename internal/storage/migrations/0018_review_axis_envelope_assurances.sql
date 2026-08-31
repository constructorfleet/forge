-- 0018_review_axis_envelope_assurances.sql: widens review_axis_envelopes'
-- raw_findings column into raw_envelope (issue #182) so a persisted axis
-- envelope carries both its raw findings and its assurances (issue #176) —
-- the JSON blob's shape becomes {"findings": [...], "assurances": [...]}
-- instead of a bare findings array. Renaming in place (rather than adding a
-- second column) keeps the "one opaque JSON blob per axis envelope"
-- convention 0017 established.
ALTER TABLE review_axis_envelopes RENAME COLUMN raw_findings TO raw_envelope;
