package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

const (
	ConflictResolutionStatusPublished = "published"
	ConflictResolutionStatusRestored  = "restored"
	ConflictResolutionStatusLostLease = "lost_lease"
)

func (s *SQLiteStore) RecordConflictResolutionAttempt(ctx context.Context, attempt ConflictResolutionAttempt) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: record conflict resolution attempt for issue %s/%s: %w", attempt.ExecutionID, attempt.IssueID, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO conflict_resolution_attempts (
			execution_id, issue_id, pr_number, branch, original_sha, candidate_sha,
			status, details, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		attempt.ExecutionID, attempt.IssueID, attempt.PRNumber, attempt.Branch,
		attempt.OriginalSHA, attempt.CandidateSHA, attempt.Status, attempt.Details,
		attempt.CreatedAt.UTC(), attempt.UpdatedAt.UTC(),
	); err != nil {
		switch {
		case isForeignKeyConstraintErr(err):
			return fmt.Errorf("storage: record conflict resolution attempt for issue %s/%s: %w", attempt.ExecutionID, attempt.IssueID, ErrNotFound)
		default:
			return fmt.Errorf("storage: record conflict resolution attempt for issue %s/%s: %w", attempt.ExecutionID, attempt.IssueID, err)
		}
	}

	if err := appendConflictResolutionEvent(ctx, tx, "conflict_resolution.published", attempt); err != nil {
		return fmt.Errorf("storage: record conflict resolution attempt for issue %s/%s: %w", attempt.ExecutionID, attempt.IssueID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: record conflict resolution attempt for issue %s/%s: %w", attempt.ExecutionID, attempt.IssueID, err)
	}
	return nil
}

func (s *SQLiteStore) ActiveConflictResolutionAttempt(ctx context.Context, executionID, issueID string) (ConflictResolutionAttempt, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT execution_id, issue_id, pr_number, branch, original_sha, candidate_sha,
			status, details, created_at, updated_at
		FROM conflict_resolution_attempts
		WHERE execution_id = ? AND issue_id = ? AND status = ?
		ORDER BY id DESC
		LIMIT 1`,
		executionID, issueID, ConflictResolutionStatusPublished,
	)

	attempt, err := scanConflictResolutionAttempt(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return ConflictResolutionAttempt{}, fmt.Errorf("storage: active conflict resolution attempt for issue %s/%s: %w", executionID, issueID, ErrNotFound)
		}
		return ConflictResolutionAttempt{}, fmt.Errorf("storage: active conflict resolution attempt for issue %s/%s: %w", executionID, issueID, err)
	}
	return attempt, nil
}

func (s *SQLiteStore) UpdateConflictResolutionAttemptStatus(ctx context.Context, executionID, issueID, status, details string, updatedAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: update conflict resolution attempt for issue %s/%s: %w", executionID, issueID, err)
	}
	defer func() { _ = tx.Rollback() }()

	var id int64
	row := tx.QueryRowContext(ctx, `
		SELECT id
		FROM conflict_resolution_attempts
		WHERE execution_id = ? AND issue_id = ? AND status = ?
		ORDER BY id DESC
		LIMIT 1`,
		executionID, issueID, ConflictResolutionStatusPublished,
	)
	if err := row.Scan(&id); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("storage: update conflict resolution attempt for issue %s/%s: %w", executionID, issueID, ErrNotFound)
		}
		return fmt.Errorf("storage: update conflict resolution attempt for issue %s/%s: %w", executionID, issueID, err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE conflict_resolution_attempts
		SET status = ?, details = ?, updated_at = ?
		WHERE id = ?`,
		status, details, updatedAt.UTC(), id,
	); err != nil {
		return fmt.Errorf("storage: update conflict resolution attempt for issue %s/%s: %w", executionID, issueID, err)
	}

	attempt := ConflictResolutionAttempt{
		ExecutionID: executionID,
		IssueID:     issueID,
		Status:      status,
		Details:     details,
		UpdatedAt:   updatedAt.UTC(),
	}
	if err := appendConflictResolutionEvent(ctx, tx, "conflict_resolution."+status, attempt); err != nil {
		return fmt.Errorf("storage: update conflict resolution attempt for issue %s/%s: %w", executionID, issueID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: update conflict resolution attempt for issue %s/%s: %w", executionID, issueID, err)
	}
	return nil
}

func scanConflictResolutionAttempt(row scanner) (ConflictResolutionAttempt, error) {
	var attempt ConflictResolutionAttempt
	if err := row.Scan(
		&attempt.ExecutionID, &attempt.IssueID, &attempt.PRNumber, &attempt.Branch,
		&attempt.OriginalSHA, &attempt.CandidateSHA, &attempt.Status, &attempt.Details,
		&attempt.CreatedAt, &attempt.UpdatedAt,
	); err != nil {
		return ConflictResolutionAttempt{}, err
	}
	attempt.CreatedAt = attempt.CreatedAt.UTC()
	attempt.UpdatedAt = attempt.UpdatedAt.UTC()
	return attempt, nil
}

func appendConflictResolutionEvent(ctx context.Context, tx *sql.Tx, eventType string, attempt ConflictResolutionAttempt) error {
	data, err := json.Marshal(struct {
		PRNumber     int    `json:"pr_number,omitempty"`
		Branch       string `json:"branch,omitempty"`
		OriginalSHA  string `json:"original_sha,omitempty"`
		CandidateSHA string `json:"candidate_sha,omitempty"`
		Status       string `json:"status"`
		Details      string `json:"details,omitempty"`
	}{
		PRNumber:     attempt.PRNumber,
		Branch:       attempt.Branch,
		OriginalSHA:  attempt.OriginalSHA,
		CandidateSHA: attempt.CandidateSHA,
		Status:       attempt.Status,
		Details:      attempt.Details,
	})
	if err != nil {
		return err
	}
	occurredAt := attempt.UpdatedAt
	if occurredAt.IsZero() {
		occurredAt = attempt.CreatedAt
	}
	return insertEvent(ctx, tx, Event{
		ExecutionID: attempt.ExecutionID,
		IssueID:     attempt.IssueID,
		Type:        eventType,
		Data:        string(data),
		OccurredAt:  occurredAt,
	})
}
