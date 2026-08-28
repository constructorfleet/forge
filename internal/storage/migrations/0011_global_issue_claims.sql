-- 0011_global_issue_claims.sql: one active claim per Issue across all
-- Executions. Issue claims model active Worker ownership, not historical
-- execution membership, so uniqueness belongs to issue_id alone.

DELETE FROM workers
WHERE id NOT IN (
    SELECT MAX(id)
    FROM workers
    GROUP BY issue_id
);

CREATE UNIQUE INDEX idx_workers_issue_claim_unique ON workers (issue_id);
