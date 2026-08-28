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
