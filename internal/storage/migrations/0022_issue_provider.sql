ALTER TABLE execution_issues
ADD COLUMN provider TEXT NOT NULL DEFAULT 'github';

ALTER TABLE dependencies
ADD COLUMN issue_provider TEXT NOT NULL DEFAULT 'github';

ALTER TABLE dependencies
ADD COLUMN depends_on_provider TEXT NOT NULL DEFAULT 'github';
