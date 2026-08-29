-- 0013_status_signal_checkpoints.sql: persists whether the ticket-24
-- status-reflection start comment (internal/statusreflect) has already been
-- posted for an Issue's READY -> CLAIMED transition. Unlike the label swap
-- (naturally idempotent via AddLabel/RemoveLabel's tracker contract), a
-- posted comment has no tracker-side dedup key, so a retry or resume needs
-- a local record of "already posted" the same way needs_info_checkpoints's
-- comment_posted column guards handleNeedsInfo's comment.
CREATE TABLE status_signal_checkpoints (
    execution_id   TEXT NOT NULL,
    issue_id       TEXT NOT NULL,
    comment_posted BOOLEAN NOT NULL,
    PRIMARY KEY (execution_id, issue_id),
    FOREIGN KEY (execution_id, issue_id) REFERENCES execution_issues (execution_id, issue_id)
);
