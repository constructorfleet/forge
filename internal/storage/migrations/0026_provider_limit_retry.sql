ALTER TABLE execution_issues
ADD COLUMN retry_provider_limit_limit INTEGER NOT NULL DEFAULT 0;

ALTER TABLE execution_issues
ADD COLUMN retry_provider_limit_used INTEGER NOT NULL DEFAULT 0;

ALTER TABLE execution_issues
ADD COLUMN provider_limit_retry_at TIMESTAMP NULL;
