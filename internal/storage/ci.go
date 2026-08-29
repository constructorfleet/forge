package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// RecordCIRun persists one CI supervision attempt and appends a "ci.run"
// Event in the same transaction.
func (s *SQLiteStore) RecordCIRun(ctx context.Context, run CIRun) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: record ci run for issue %s/%s: %w", run.ExecutionID, run.IssueID, err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO ci_runs (execution_id, issue_id, status, kind, check_name, details, checked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		run.ExecutionID, run.IssueID, string(run.Status), string(run.Kind), run.CheckName, run.Details, run.CheckedAt.UTC(),
	)
	if err != nil {
		switch {
		case isForeignKeyConstraintErr(err):
			return fmt.Errorf("storage: record ci run for issue %s/%s: %w", run.ExecutionID, run.IssueID, ErrNotFound)
		default:
			return fmt.Errorf("storage: record ci run for issue %s/%s: %w", run.ExecutionID, run.IssueID, err)
		}
	}
	if err := appendCIRunEvent(ctx, tx, run); err != nil {
		return fmt.Errorf("storage: record ci run for issue %s/%s: %w", run.ExecutionID, run.IssueID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: record ci run for issue %s/%s: %w", run.ExecutionID, run.IssueID, err)
	}
	return nil
}

func appendCIRunEvent(ctx context.Context, tx *sql.Tx, run CIRun) error {
	data, err := json.Marshal(struct {
		Status    string `json:"status"`
		Kind      string `json:"kind,omitempty"`
		CheckName string `json:"check_name,omitempty"`
		Details   string `json:"details,omitempty"`
	}{
		Status:    string(run.Status),
		Kind:      string(run.Kind),
		CheckName: run.CheckName,
		Details:   run.Details,
	})
	if err != nil {
		return err
	}
	return insertEvent(ctx, tx, Event{
		ExecutionID: run.ExecutionID,
		IssueID:     run.IssueID,
		Type:        "ci.run",
		Data:        string(data),
		OccurredAt:  run.CheckedAt,
	})
}

// CIRunsByIssue returns every persisted CI supervision attempt for one
// Issue within an Execution, ordered by insertion.
func (s *SQLiteStore) CIRunsByIssue(ctx context.Context, executionID, issueID string) ([]CIRun, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT execution_id, issue_id, status, kind, check_name, details, checked_at
		FROM ci_runs
		WHERE execution_id = ? AND issue_id = ?
		ORDER BY id`,
		executionID, issueID,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: ci runs for issue %s/%s: %w", executionID, issueID, err)
	}
	defer func() { _ = rows.Close() }()

	var runs []CIRun
	for rows.Next() {
		var run CIRun
		var status, kind string
		if err := rows.Scan(&run.ExecutionID, &run.IssueID, &status, &kind, &run.CheckName, &run.Details, &run.CheckedAt); err != nil {
			return nil, fmt.Errorf("storage: scan ci run: %w", err)
		}
		run.Status = CIRunStatus(status)
		run.Kind = CIRunKind(kind)
		run.CheckedAt = run.CheckedAt.UTC()
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: ci runs for issue %s/%s: %w", executionID, issueID, err)
	}
	return runs, nil
}
