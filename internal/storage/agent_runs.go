package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// RecordAgentRun persists one implementation Agent invocation and appends
// an "agent.run" Event in the same transaction.
func (s *SQLiteStore) RecordAgentRun(ctx context.Context, run AgentRun) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: record agent run for issue %s/%s: %w", run.ExecutionID, run.IssueID, err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO agent_runs (execution_id, issue_id, backend, started_at, finished_at, result, context_bytes, input_tokens, output_tokens)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ExecutionID, run.IssueID, run.Backend, run.StartedAt.UTC(), run.FinishedAt.UTC(), run.Result, run.ContextBytes, run.InputTokens, run.OutputTokens,
	)
	if err != nil {
		switch {
		case isForeignKeyConstraintErr(err):
			return fmt.Errorf("storage: record agent run for issue %s/%s: %w", run.ExecutionID, run.IssueID, ErrNotFound)
		default:
			return fmt.Errorf("storage: record agent run for issue %s/%s: %w", run.ExecutionID, run.IssueID, err)
		}
	}
	if err := appendAgentRunEvent(ctx, tx, run); err != nil {
		return fmt.Errorf("storage: record agent run for issue %s/%s: %w", run.ExecutionID, run.IssueID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: record agent run for issue %s/%s: %w", run.ExecutionID, run.IssueID, err)
	}
	return nil
}

func appendAgentRunEvent(ctx context.Context, tx *sql.Tx, run AgentRun) error {
	data, err := json.Marshal(struct {
		Backend      string `json:"backend"`
		Result       string `json:"result"`
		ContextBytes int    `json:"context_bytes"`
		InputTokens  *int   `json:"input_tokens,omitempty"`
		OutputTokens *int   `json:"output_tokens,omitempty"`
	}{
		Backend:      run.Backend,
		Result:       run.Result,
		ContextBytes: run.ContextBytes,
		InputTokens:  run.InputTokens,
		OutputTokens: run.OutputTokens,
	})
	if err != nil {
		return err
	}
	return insertEvent(ctx, tx, Event{
		ExecutionID: run.ExecutionID,
		IssueID:     run.IssueID,
		Type:        "agent.run",
		Data:        string(data),
		OccurredAt:  run.FinishedAt,
	})
}

// AgentRunsByExecution returns every AgentRun recorded for executionID in
// insertion order.
func (s *SQLiteStore) AgentRunsByExecution(ctx context.Context, executionID string) ([]AgentRun, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT execution_id, issue_id, backend, started_at, finished_at, result, context_bytes, input_tokens, output_tokens
		FROM agent_runs
		WHERE execution_id = ?
		ORDER BY id`,
		executionID,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: agent runs for execution %s: %w", executionID, err)
	}
	return scanAgentRuns(rows, "storage: agent runs for execution "+executionID)
}

// AgentRunsByIssue returns every AgentRun recorded for one Issue within an
// Execution, ordered by insertion.
func (s *SQLiteStore) AgentRunsByIssue(ctx context.Context, executionID, issueID string) ([]AgentRun, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT execution_id, issue_id, backend, started_at, finished_at, result, context_bytes, input_tokens, output_tokens
		FROM agent_runs
		WHERE execution_id = ? AND issue_id = ?
		ORDER BY id`,
		executionID, issueID,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: agent runs for issue %s/%s: %w", executionID, issueID, err)
	}
	return scanAgentRuns(rows, fmt.Sprintf("storage: agent runs for issue %s/%s", executionID, issueID))
}

func scanAgentRuns(rows *sql.Rows, contextMsg string) ([]AgentRun, error) {
	defer func() { _ = rows.Close() }()

	var runs []AgentRun
	for rows.Next() {
		var (
			run                   AgentRun
			startedAt, finishedAt time.Time
			inputTokens           sql.NullInt64
			outputTokens          sql.NullInt64
		)
		if err := rows.Scan(
			&run.ExecutionID, &run.IssueID, &run.Backend, &startedAt, &finishedAt,
			&run.Result, &run.ContextBytes, &inputTokens, &outputTokens,
		); err != nil {
			return nil, fmt.Errorf("%s: scan agent run: %w", contextMsg, err)
		}
		run.StartedAt = startedAt.UTC()
		run.FinishedAt = finishedAt.UTC()
		if inputTokens.Valid {
			v := int(inputTokens.Int64)
			run.InputTokens = &v
		}
		if outputTokens.Valid {
			v := int(outputTokens.Int64)
			run.OutputTokens = &v
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", contextMsg, err)
	}
	return runs, nil
}
