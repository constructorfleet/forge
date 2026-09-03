package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// RecordAgentRun persists one implementation Agent invocation and appends
// an "agent.run" Event in the same transaction, returning the
// storage-assigned agent_runs id so callers (internal/engine) can key
// TranscriptEvents to this specific attempt via RecordTranscriptEvents.
func (s *SQLiteStore) RecordAgentRun(ctx context.Context, run AgentRun) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("storage: record agent run for issue %s/%s: %w", run.ExecutionID, run.IssueID, err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO agent_runs (execution_id, issue_id, backend, started_at, finished_at, result, context_bytes, input_tokens, output_tokens)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ExecutionID, run.IssueID, run.Backend, run.StartedAt.UTC(), run.FinishedAt.UTC(), run.Result, run.ContextBytes, run.InputTokens, run.OutputTokens,
	)
	if err != nil {
		switch {
		case isForeignKeyConstraintErr(err):
			return 0, fmt.Errorf("storage: record agent run for issue %s/%s: %w", run.ExecutionID, run.IssueID, ErrNotFound)
		default:
			return 0, fmt.Errorf("storage: record agent run for issue %s/%s: %w", run.ExecutionID, run.IssueID, err)
		}
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("storage: record agent run for issue %s/%s: %w", run.ExecutionID, run.IssueID, err)
	}
	if err := appendAgentRunEvent(ctx, tx, run); err != nil {
		return 0, fmt.Errorf("storage: record agent run for issue %s/%s: %w", run.ExecutionID, run.IssueID, err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("storage: record agent run for issue %s/%s: %w", run.ExecutionID, run.IssueID, err)
	}
	return id, nil
}

// AgentRunResultRunning is the result recorded for an AgentRun row between
// StartAgentRun and FinalizeAgentRun (issue 36). A run left with this
// result is one whose process died before it could be finalized — a
// durable "interrupted" marker rather than a lost record.
const AgentRunResultRunning = "RUNNING"

// StartAgentRun inserts an in-progress AgentRun row up front and returns its
// id, so a caller can persist transcript events against it incrementally as
// the Agent streams (issue 36). The row records AgentRunResultRunning and a
// placeholder finished_at (= StartedAt) until FinalizeAgentRun overwrites
// them; no "agent.run" Event is appended here (see FinalizeAgentRun).
func (s *SQLiteStore) StartAgentRun(ctx context.Context, run AgentRun) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO agent_runs (execution_id, issue_id, backend, started_at, finished_at, result, context_bytes, input_tokens, output_tokens)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ExecutionID, run.IssueID, run.Backend, run.StartedAt.UTC(), run.StartedAt.UTC(), AgentRunResultRunning, run.ContextBytes, nil, nil,
	)
	if err != nil {
		switch {
		case isForeignKeyConstraintErr(err):
			return 0, fmt.Errorf("storage: start agent run for issue %s/%s: %w", run.ExecutionID, run.IssueID, ErrNotFound)
		default:
			return 0, fmt.Errorf("storage: start agent run for issue %s/%s: %w", run.ExecutionID, run.IssueID, err)
		}
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("storage: start agent run for issue %s/%s: %w", run.ExecutionID, run.IssueID, err)
	}
	return id, nil
}

// FinalizeAgentRun updates the AgentRun row StartAgentRun created
// (agentRunID) with its terminal result, finished_at, and token usage, and
// appends the "agent.run" Event — the completion half of the lifecycle. It
// is the analogue of RecordAgentRun's event append, deferred to run
// completion so the audit log still marks a finished run exactly once.
func (s *SQLiteStore) FinalizeAgentRun(ctx context.Context, agentRunID int64, run AgentRun) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: finalize agent run %d: %w", agentRunID, err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		UPDATE agent_runs
		SET finished_at = ?, result = ?, input_tokens = ?, output_tokens = ?
		WHERE id = ?`,
		run.FinishedAt.UTC(), run.Result, run.InputTokens, run.OutputTokens, agentRunID,
	)
	if err != nil {
		return fmt.Errorf("storage: finalize agent run %d: %w", agentRunID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: finalize agent run %d: %w", agentRunID, err)
	}
	if affected == 0 {
		return fmt.Errorf("storage: finalize agent run %d: %w", agentRunID, ErrNotFound)
	}
	if err := appendAgentRunEvent(ctx, tx, run); err != nil {
		return fmt.Errorf("storage: finalize agent run %d: %w", agentRunID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: finalize agent run %d: %w", agentRunID, err)
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
func (s *SQLiteStore) LiveRuns(ctx context.Context) ([]LiveRun, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, execution_id, issue_id
		FROM agent_runs
		ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("storage: live runs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var runs []LiveRun
	for rows.Next() {
		var run LiveRun
		if err := rows.Scan(&run.AgentRunID, &run.ExecutionID, &run.IssueID); err != nil {
			return nil, fmt.Errorf("storage: live runs: scan: %w", err)
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: live runs: %w", err)
	}
	return runs, nil
}

// AgentRunsByExecution returns every AgentRun recorded for executionID in
// insertion order.
func (s *SQLiteStore) AgentRunsByExecution(ctx context.Context, executionID string) ([]AgentRun, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, execution_id, issue_id, backend, started_at, finished_at, result, context_bytes, input_tokens, output_tokens
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
		SELECT id, execution_id, issue_id, backend, started_at, finished_at, result, context_bytes, input_tokens, output_tokens
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
			&run.ID, &run.ExecutionID, &run.IssueID, &run.Backend, &startedAt, &finishedAt,
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
