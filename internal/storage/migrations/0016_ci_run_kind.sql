-- 0016_ci_run_kind.sql: extends ci_runs (issue 109, PR supervision) with a
-- `kind` discriminator so one row can represent a required-check failure
-- ("check", the pre-existing and default behavior), an actionable PR review
-- requesting changes ("review"), or a detected merge conflict ("conflict").
-- Empty defaults every pre-existing row to "check" implicitly.
ALTER TABLE ci_runs ADD COLUMN kind TEXT NOT NULL DEFAULT '';
