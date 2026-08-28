package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Teagan42/forge/internal/domain"
)

// CreateExecution persists a new Execution.
func (s *SQLiteStore) CreateExecution(ctx context.Context, exec domain.Execution) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO executions (id, base_revision, started_at)
		VALUES (?, ?, ?)`,
		exec.ID, exec.BaseRevision, exec.StartedAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("storage: create execution %s: %w", exec.ID, err)
	}
	return nil
}

// LoadExecution reloads an Execution and every Issue recorded against it.
func (s *SQLiteStore) LoadExecution(ctx context.Context, executionID string) (ExecutionState, error) {
	exec, err := s.getExecution(ctx, s.db, executionID)
	if err != nil {
		return ExecutionState{}, err
	}
	issues, err := s.ListIssues(ctx, executionID)
	if err != nil {
		return ExecutionState{}, err
	}
	return ExecutionState{Execution: exec, Issues: issues}, nil
}

// ListExecutions reloads every persisted Execution together with its
// Issues, ordered by started_at then id for stable CLI output.
func (s *SQLiteStore) ListExecutions(ctx context.Context) ([]ExecutionState, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, base_revision, started_at
		FROM executions
		ORDER BY started_at, id`)
	if err != nil {
		return nil, fmt.Errorf("storage: list executions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var executions []domain.Execution
	for rows.Next() {
		var (
			exec      domain.Execution
			startedAt time.Time
		)
		if err := rows.Scan(&exec.ID, &exec.BaseRevision, &startedAt); err != nil {
			return nil, fmt.Errorf("storage: list executions: %w", err)
		}
		exec.StartedAt = startedAt.UTC()
		executions = append(executions, exec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: list executions: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("storage: list executions: %w", err)
	}

	states := make([]ExecutionState, 0, len(executions))
	for _, exec := range executions {
		issues, err := s.ListIssues(ctx, exec.ID)
		if err != nil {
			return nil, err
		}
		states = append(states, ExecutionState{Execution: exec, Issues: issues})
	}
	return states, nil
}

func (s *SQLiteStore) getExecution(ctx context.Context, q querier, executionID string) (domain.Execution, error) {
	row := q.QueryRowContext(ctx, `
		SELECT id, base_revision, started_at FROM executions WHERE id = ?`,
		executionID,
	)
	var (
		exec      domain.Execution
		startedAt time.Time
	)
	if err := row.Scan(&exec.ID, &exec.BaseRevision, &startedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Execution{}, fmt.Errorf("storage: execution %s: %w", executionID, ErrNotFound)
		}
		return domain.Execution{}, fmt.Errorf("storage: load execution %s: %w", executionID, err)
	}
	exec.StartedAt = startedAt.UTC()
	return exec, nil
}
