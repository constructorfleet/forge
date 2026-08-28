package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// RecordGateRun persists one executed Quality Gate Result and appends a
// "gate.run" Event, both inside a single database transaction, so a
// gate_runs row and its corresponding audit-log Event never diverge.
func (s *SQLiteStore) RecordGateRun(ctx context.Context, run GateRun) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: record gate run %s for issue %s/%s: %w", run.Name, run.ExecutionID, run.IssueID, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO gate_runs (execution_id, issue_id, name, command, passed, started_at, ran_at, exit_code, stdout, stderr)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ExecutionID, run.IssueID, run.Name, run.Command, run.Passed,
		run.StartedAt.UTC(), run.FinishedAt.UTC(), run.ExitCode, run.Stdout, run.Stderr,
	); err != nil {
		switch {
		case isForeignKeyConstraintErr(err):
			return fmt.Errorf("storage: record gate run %s for issue %s/%s: %w", run.Name, run.ExecutionID, run.IssueID, ErrNotFound)
		default:
			return fmt.Errorf("storage: record gate run %s for issue %s/%s: %w", run.Name, run.ExecutionID, run.IssueID, err)
		}
	}

	if err := appendGateRunEvent(ctx, tx, run); err != nil {
		return fmt.Errorf("storage: record gate run %s for issue %s/%s: %w", run.Name, run.ExecutionID, run.IssueID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: record gate run %s for issue %s/%s: %w", run.Name, run.ExecutionID, run.IssueID, err)
	}
	return nil
}

// appendGateRunEvent records a "gate.run" Event with a JSON-encoded
// summary of run. The full captured stdout/stderr live in gate_runs, not
// duplicated into the event log; the event carries only what's needed to
// scan the audit trail (name, command, exit code, pass/fail).
func appendGateRunEvent(ctx context.Context, tx *sql.Tx, run GateRun) error {
	data, err := json.Marshal(struct {
		Name     string `json:"name"`
		Command  string `json:"command"`
		ExitCode int    `json:"exit_code"`
		Passed   bool   `json:"passed"`
	}{Name: run.Name, Command: run.Command, ExitCode: run.ExitCode, Passed: run.Passed})
	if err != nil {
		return err
	}
	return insertEvent(ctx, tx, Event{
		ExecutionID: run.ExecutionID,
		IssueID:     run.IssueID,
		Type:        "gate.run",
		Data:        string(data),
		OccurredAt:  run.FinishedAt,
	})
}

// GateRunsByIssue returns every GateRun recorded for one Issue within an
// Execution, ordered by primary key (i.e. insertion/execution order).
func (s *SQLiteStore) GateRunsByIssue(ctx context.Context, executionID, issueID string) ([]GateRun, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT execution_id, issue_id, name, command, started_at, ran_at, exit_code, stdout, stderr, passed
		FROM gate_runs
		WHERE execution_id = ? AND issue_id = ?
		ORDER BY id`,
		executionID, issueID,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: gate runs for issue %s/%s: %w", executionID, issueID, err)
	}
	defer func() { _ = rows.Close() }()

	var runs []GateRun
	for rows.Next() {
		var (
			run                   GateRun
			startedAt, finishedAt time.Time
		)
		if err := rows.Scan(
			&run.ExecutionID, &run.IssueID, &run.Name, &run.Command,
			&startedAt, &finishedAt, &run.ExitCode, &run.Stdout, &run.Stderr, &run.Passed,
		); err != nil {
			return nil, fmt.Errorf("storage: scan gate run: %w", err)
		}
		run.StartedAt = startedAt.UTC()
		run.FinishedAt = finishedAt.UTC()
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: gate runs for issue %s/%s: %w", executionID, issueID, err)
	}
	return runs, nil
}
