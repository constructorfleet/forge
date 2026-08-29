-- 0013_issue_body.sql: adds the Issue's Body to execution_issues (ticket
-- 19). The Agent prompt needs the Issue's full title and body — the title
-- alone (0005_issue_title.sql) isn't enough context to implement the
-- Issue's requirements. Body is persisted for the same reason Title is:
-- Store.TransitionIssue always reloads the Issue fresh from this table, so
-- an unpersisted Body would be silently dropped after the first transition.

ALTER TABLE execution_issues ADD COLUMN body TEXT NOT NULL DEFAULT '';
