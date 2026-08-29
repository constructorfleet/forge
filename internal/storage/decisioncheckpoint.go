package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// SaveDecisionCheckpoint persists checkpoint, inserting a new row or
// replacing the existing one for (ExecutionID, DecisionID) -- the
// NEEDS_HUMAN handling in internal/wayfinding calls this both when a
// Decision first pauses and again once its comment has posted, mirroring
// SaveNeedsInfoCheckpoint's two-step intent-then-post pattern.
func (s *SQLiteStore) SaveDecisionCheckpoint(ctx context.Context, checkpoint DecisionCheckpoint) error {
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
		INSERT INTO decision_checkpoints (
			execution_id, decision_id, decision_revision, question, context,
			label_added, comment_posted, comment_author, comment_posted_at,
			created_at, resumed_at, resumed_context
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (execution_id, decision_id) DO UPDATE SET
			decision_revision = excluded.decision_revision,
			question = excluded.question,
			context = excluded.context,
			label_added = excluded.label_added,
			comment_posted = excluded.comment_posted,
			comment_author = excluded.comment_author,
			comment_posted_at = excluded.comment_posted_at,
			created_at = excluded.created_at,
			resumed_at = excluded.resumed_at,
			resumed_context = excluded.resumed_context`,
		checkpoint.ExecutionID, checkpoint.DecisionID, checkpoint.DecisionRevision, checkpoint.Question, contextVal,
		checkpoint.LabelAdded, checkpoint.CommentPosted, commentAuthor, commentPostedAt,
		checkpoint.CreatedAt.UTC(), resumedAt, resumedContext,
	)
	if err != nil {
		return fmt.Errorf("storage: save decision checkpoint %s/%s: %w", checkpoint.ExecutionID, checkpoint.DecisionID, err)
	}
	return nil
}

// GetDecisionCheckpoint reloads the NEEDS_HUMAN checkpoint for
// (executionID, decisionID). Returns ErrNotFound if none has been
// recorded.
func (s *SQLiteStore) GetDecisionCheckpoint(ctx context.Context, executionID, decisionID string) (DecisionCheckpoint, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT decision_revision, question, context, label_added, comment_posted, comment_author, comment_posted_at,
			created_at, resumed_at, resumed_context
		FROM decision_checkpoints WHERE execution_id = ? AND decision_id = ?`,
		executionID, decisionID,
	)

	var (
		checkpoint      DecisionCheckpoint
		contextVal      sql.NullString
		commentAuthor   sql.NullString
		commentPostedAt sql.NullTime
		createdAt       time.Time
		resumedAt       sql.NullTime
		resumedContext  sql.NullString
	)
	if err := row.Scan(
		&checkpoint.DecisionRevision, &checkpoint.Question, &contextVal, &checkpoint.LabelAdded, &checkpoint.CommentPosted,
		&commentAuthor, &commentPostedAt,
		&createdAt, &resumedAt, &resumedContext,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DecisionCheckpoint{}, fmt.Errorf("storage: decision checkpoint %s/%s: %w", executionID, decisionID, ErrNotFound)
		}
		return DecisionCheckpoint{}, fmt.Errorf("storage: load decision checkpoint %s/%s: %w", executionID, decisionID, err)
	}

	checkpoint.ExecutionID = executionID
	checkpoint.DecisionID = decisionID
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

// GetDecisionCheckpointsByExecution reloads all NEEDS_HUMAN checkpoints
// for a Planning Execution.
func (s *SQLiteStore) GetDecisionCheckpointsByExecution(ctx context.Context, executionID string) ([]DecisionCheckpoint, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT decision_id, decision_revision, question, context, label_added, comment_posted, comment_author, comment_posted_at,
			created_at, resumed_at, resumed_context
		FROM decision_checkpoints WHERE execution_id = ?`,
		executionID,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: list decision checkpoints for execution %s: %w", executionID, err)
	}
	defer func() { _ = rows.Close() }()

	var checkpoints []DecisionCheckpoint
	for rows.Next() {
		var (
			checkpoint      DecisionCheckpoint
			contextVal      sql.NullString
			commentAuthor   sql.NullString
			commentPostedAt sql.NullTime
			createdAt       time.Time
			resumedAt       sql.NullTime
			resumedContext  sql.NullString
		)
		if err := rows.Scan(
			&checkpoint.DecisionID, &checkpoint.DecisionRevision, &checkpoint.Question, &contextVal, &checkpoint.LabelAdded, &checkpoint.CommentPosted,
			&commentAuthor, &commentPostedAt,
			&createdAt, &resumedAt, &resumedContext,
		); err != nil {
			return nil, fmt.Errorf("storage: scan decision checkpoint for execution %s: %w", executionID, err)
		}

		checkpoint.ExecutionID = executionID
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
		checkpoints = append(checkpoints, checkpoint)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate decision checkpoints for execution %s: %w", executionID, err)
	}
	return checkpoints, nil
}