package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// SaveNeedsInfoCheckpoint persists checkpoint, inserting a new row or
// replacing the existing one for (ExecutionID, IssueID) — the NEEDS_INFO
// handling in internal/engine calls this both when an Issue first enters
// NEEDS_INFO and again when `forge resume` records that it was resumed.
func (s *SQLiteStore) SaveNeedsInfoCheckpoint(ctx context.Context, checkpoint NeedsInfoCheckpoint) error {
	var resumedAt any
	if checkpoint.ResumedAt != nil {
		resumedAt = checkpoint.ResumedAt.UTC()
	}
	var resumedContext any
	if checkpoint.ResumedContext != "" {
		resumedContext = checkpoint.ResumedContext
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO needs_info_checkpoints (
			execution_id, issue_id, question, reason,
			label_added, comment_posted, created_at, resumed_at, resumed_context
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (execution_id, issue_id) DO UPDATE SET
			question = excluded.question,
			reason = excluded.reason,
			label_added = excluded.label_added,
			comment_posted = excluded.comment_posted,
			created_at = excluded.created_at,
			resumed_at = excluded.resumed_at,
			resumed_context = excluded.resumed_context`,
		checkpoint.ExecutionID, checkpoint.IssueID, checkpoint.Question, checkpoint.Reason,
		checkpoint.LabelAdded, checkpoint.CommentPosted, checkpoint.CreatedAt.UTC(), resumedAt, resumedContext,
	)
	if err != nil {
		return fmt.Errorf("storage: save needs-info checkpoint %s/%s: %w", checkpoint.ExecutionID, checkpoint.IssueID, err)
	}
	return nil
}

// GetNeedsInfoCheckpoint reloads the needs-info checkpoint for
// (executionID, issueID). Returns ErrNotFound if none has been recorded.
func (s *SQLiteStore) GetNeedsInfoCheckpoint(ctx context.Context, executionID, issueID string) (NeedsInfoCheckpoint, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT question, reason, label_added, comment_posted, created_at, resumed_at, resumed_context
		FROM needs_info_checkpoints WHERE execution_id = ? AND issue_id = ?`,
		executionID, issueID,
	)

	var (
		checkpoint     NeedsInfoCheckpoint
		reason         sql.NullString
		createdAt      time.Time
		resumedAt      sql.NullTime
		resumedContext sql.NullString
	)
	if err := row.Scan(
		&checkpoint.Question, &reason, &checkpoint.LabelAdded, &checkpoint.CommentPosted,
		&createdAt, &resumedAt, &resumedContext,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NeedsInfoCheckpoint{}, fmt.Errorf("storage: needs-info checkpoint %s/%s: %w", executionID, issueID, ErrNotFound)
		}
		return NeedsInfoCheckpoint{}, fmt.Errorf("storage: load needs-info checkpoint %s/%s: %w", executionID, issueID, err)
	}

	checkpoint.ExecutionID = executionID
	checkpoint.IssueID = issueID
	checkpoint.Reason = reason.String
	checkpoint.CreatedAt = createdAt.UTC()
	if resumedAt.Valid {
		t := resumedAt.Time.UTC()
		checkpoint.ResumedAt = &t
	}
	checkpoint.ResumedContext = resumedContext.String
	return checkpoint, nil
}
