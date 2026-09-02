package storage

import (
	"context"
	"fmt"

	"github.com/Teagan42/forge/internal/domain"
)

// RecordReviewOverride persists override, keyed by (issue_id, signature).
// Recording the same Signature for the same IssueID again is a no-op (the
// existing row's Reason/CreatedAt are kept) rather than an error, since
// escalateReviewToNeedsInfo may run more than once for the same Issue.
func (s *SQLiteStore) RecordReviewOverride(ctx context.Context, override domain.ReviewOverride) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO review_overrides (issue_id, signature, axis, file, line, message, reason, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (issue_id, signature) DO NOTHING`,
		override.IssueID, override.Signature, override.Axis, override.File, override.Line,
		override.Message, override.Reason, override.CreatedAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("storage: record review override for issue %s: %w", override.IssueID, err)
	}
	return nil
}

// ReviewOverridesByIssue returns every ReviewOverride recorded for issueID,
// across all Executions, ordered by insertion.
func (s *SQLiteStore) ReviewOverridesByIssue(ctx context.Context, issueID string) ([]domain.ReviewOverride, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT issue_id, signature, axis, file, line, message, reason, created_at
		FROM review_overrides
		WHERE issue_id = ?
		ORDER BY id`,
		issueID,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: review overrides for issue %s: %w", issueID, err)
	}
	defer func() { _ = rows.Close() }()

	var overrides []domain.ReviewOverride
	for rows.Next() {
		var o domain.ReviewOverride
		if err := rows.Scan(&o.IssueID, &o.Signature, &o.Axis, &o.File, &o.Line, &o.Message, &o.Reason, &o.CreatedAt); err != nil {
			return nil, fmt.Errorf("storage: review overrides for issue %s: scan: %w", issueID, err)
		}
		overrides = append(overrides, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: review overrides for issue %s: %w", issueID, err)
	}
	return overrides, nil
}
