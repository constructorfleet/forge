package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// SaveStatusSignalCheckpoint persists checkpoint, inserting a new row or
// replacing the existing one for (ExecutionID, IssueID) — internal/engine's
// transition calls this once the ticket-24 status-reflection start comment
// has been posted, so a retry never double-posts it (see
// statusreflect.IsStartTransition's doc comment for why the comment, unlike
// the label swap, needs this).
func (s *SQLiteStore) SaveStatusSignalCheckpoint(ctx context.Context, checkpoint StatusSignalCheckpoint) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO status_signal_checkpoints (execution_id, issue_id, comment_posted)
		VALUES (?, ?, ?)
		ON CONFLICT (execution_id, issue_id) DO UPDATE SET
			comment_posted = excluded.comment_posted`,
		checkpoint.ExecutionID, checkpoint.IssueID, checkpoint.CommentPosted,
	)
	if err != nil {
		return fmt.Errorf("storage: save status signal checkpoint %s/%s: %w", checkpoint.ExecutionID, checkpoint.IssueID, err)
	}
	return nil
}

// GetStatusSignalCheckpoint reloads the status-signal checkpoint for
// (executionID, issueID). Returns ErrNotFound if none has been recorded.
func (s *SQLiteStore) GetStatusSignalCheckpoint(ctx context.Context, executionID, issueID string) (StatusSignalCheckpoint, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT comment_posted FROM status_signal_checkpoints WHERE execution_id = ? AND issue_id = ?`,
		executionID, issueID,
	)

	var checkpoint StatusSignalCheckpoint
	if err := row.Scan(&checkpoint.CommentPosted); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StatusSignalCheckpoint{}, fmt.Errorf("storage: status signal checkpoint %s/%s: %w", executionID, issueID, ErrNotFound)
		}
		return StatusSignalCheckpoint{}, fmt.Errorf("storage: load status signal checkpoint %s/%s: %w", executionID, issueID, err)
	}

	checkpoint.ExecutionID = executionID
	checkpoint.IssueID = issueID
	return checkpoint, nil
}
