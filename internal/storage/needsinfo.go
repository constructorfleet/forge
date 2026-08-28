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
	var contextVal any
	if checkpoint.Context != "" {
		contextVal = checkpoint.Context
	}
	var commentAuthor any
	if checkpoint.CommentAuthor != "" {
		commentAuthor = checkpoint.CommentAuthor
	}
	var commentPostedAt any
	if !checkpoint.CommentPostedAt.IsZero() {
		commentPostedAt = checkpoint.CommentPostedAt.UTC()
	}
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
			execution_id, issue_id, question, context,
			label_added, comment_posted, comment_author, comment_posted_at,
			created_at, resumed_at, resumed_context
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (execution_id, issue_id) DO UPDATE SET
			question = excluded.question,
			context = excluded.context,
			label_added = excluded.label_added,
			comment_posted = excluded.comment_posted,
			comment_author = excluded.comment_author,
			comment_posted_at = excluded.comment_posted_at,
			created_at = excluded.created_at,
			resumed_at = excluded.resumed_at,
			resumed_context = excluded.resumed_context`,
		checkpoint.ExecutionID, checkpoint.IssueID, checkpoint.Question, contextVal,
		checkpoint.LabelAdded, checkpoint.CommentPosted, commentAuthor, commentPostedAt,
		checkpoint.CreatedAt.UTC(), resumedAt, resumedContext,
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
		SELECT question, context, label_added, comment_posted, comment_author, comment_posted_at,
			created_at, resumed_at, resumed_context
		FROM needs_info_checkpoints WHERE execution_id = ? AND issue_id = ?`,
		executionID, issueID,
	)

	var (
		checkpoint      NeedsInfoCheckpoint
		contextVal      sql.NullString
		commentAuthor   sql.NullString
		commentPostedAt sql.NullTime
		createdAt       time.Time
		resumedAt       sql.NullTime
		resumedContext  sql.NullString
	)
	if err := row.Scan(
		&checkpoint.Question, &contextVal, &checkpoint.LabelAdded, &checkpoint.CommentPosted,
		&commentAuthor, &commentPostedAt,
		&createdAt, &resumedAt, &resumedContext,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NeedsInfoCheckpoint{}, fmt.Errorf("storage: needs-info checkpoint %s/%s: %w", executionID, issueID, ErrNotFound)
		}
		return NeedsInfoCheckpoint{}, fmt.Errorf("storage: load needs-info checkpoint %s/%s: %w", executionID, issueID, err)
	}

	checkpoint.ExecutionID = executionID
	checkpoint.IssueID = issueID
	checkpoint.Context = contextVal.String
	checkpoint.CommentAuthor = commentAuthor.String
	if commentPostedAt.Valid {
		checkpoint.CommentPostedAt = commentPostedAt.Time.UTC()
	}
	checkpoint.CreatedAt = createdAt.UTC()
	if resumedAt.Valid {
		t := resumedAt.Time.UTC()
		checkpoint.ResumedAt = &t
	}
	checkpoint.ResumedContext = resumedContext.String
	return checkpoint, nil
}
