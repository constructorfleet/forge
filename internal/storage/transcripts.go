package storage

import (
	"context"
	"database/sql"
	"fmt"
)

// RecordTranscriptEvents persists every event captured during one AgentRun
// (agentRunID, as returned by RecordAgentRun), in a single transaction. A
// no-op when events is empty, so callers can always call it unconditionally
// after an Agent invocation regardless of whether anything was captured.
func (s *SQLiteStore) RecordTranscriptEvents(ctx context.Context, executionID, issueID string, agentRunID int64, events []TranscriptEvent) error {
	if len(events) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: record transcript events for issue %s/%s: %w", executionID, issueID, err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := insertTranscriptEvents(ctx, tx, executionID, issueID, agentRunID, events); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: record transcript events for issue %s/%s: %w", executionID, issueID, err)
	}
	return nil
}

// ReplaceTranscriptEvents overwrites the persisted transcript for one
// AgentRun with events (issue 36's incremental capture flush): it deletes
// every row already stored for agentRunID, then inserts events in order,
// all in one transaction. Unlike RecordTranscriptEvents, an empty events
// slice is meaningful — it clears the run's transcript — so this is not a
// no-op on empty input.
func (s *SQLiteStore) ReplaceTranscriptEvents(ctx context.Context, executionID, issueID string, agentRunID int64, events []TranscriptEvent) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: replace transcript events for issue %s/%s: %w", executionID, issueID, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM transcript_events WHERE agent_run_id = ?`, agentRunID); err != nil {
		return fmt.Errorf("storage: replace transcript events for issue %s/%s: %w", executionID, issueID, err)
	}
	if err := insertTranscriptEvents(ctx, tx, executionID, issueID, agentRunID, events); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: replace transcript events for issue %s/%s: %w", executionID, issueID, err)
	}
	return nil
}

// insertTranscriptEvents inserts events for one AgentRun using tx, shared by
// RecordTranscriptEvents (append) and ReplaceTranscriptEvents (delete then
// insert). An empty slice inserts nothing.
func insertTranscriptEvents(ctx context.Context, tx *sql.Tx, executionID, issueID string, agentRunID int64, events []TranscriptEvent) error {
	if len(events) == 0 {
		return nil
	}
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO transcript_events
			(execution_id, issue_id, agent_run_id, seq, type, role, text, tool_name, tool_input, tool_output, tool_call_id, occurred_at, phase, subagent)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("storage: insert transcript events for issue %s/%s: %w", executionID, issueID, err)
	}
	defer func() { _ = stmt.Close() }()

	for _, event := range events {
		if _, err := stmt.ExecContext(ctx,
			executionID, issueID, agentRunID, event.Seq, event.Type, event.Role, event.Text,
			event.ToolName, event.ToolInput, event.ToolOutput, event.ToolCallID, event.OccurredAt.UTC(),
			event.Phase, event.Subagent,
		); err != nil {
			return fmt.Errorf("storage: insert transcript events for issue %s/%s: %w", executionID, issueID, err)
		}
	}
	return nil
}

// TranscriptEventsByAgentRun returns every TranscriptEvent recorded for one
// AgentRun (attempt), ordered by Seq.
func (s *SQLiteStore) TranscriptEventsByAgentRun(ctx context.Context, executionID, issueID string, agentRunID int64) ([]TranscriptEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT execution_id, issue_id, agent_run_id, seq, type, role, text, tool_name, tool_input, tool_output, tool_call_id, occurred_at, phase, subagent
		FROM transcript_events
		WHERE execution_id = ? AND issue_id = ? AND agent_run_id = ?
		ORDER BY seq`,
		executionID, issueID, agentRunID,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: transcript events for issue %s/%s run %d: %w", executionID, issueID, agentRunID, err)
	}
	return scanTranscriptEvents(rows, fmt.Sprintf("storage: transcript events for issue %s/%s run %d", executionID, issueID, agentRunID))
}

// TranscriptEventsByIssue returns every TranscriptEvent recorded for an
// Issue across every AgentRun (attempt), ordered by AgentRunID then Seq —
// i.e. chronological order across the Issue's full history.
func (s *SQLiteStore) TranscriptEventsByIssue(ctx context.Context, executionID, issueID string) ([]TranscriptEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT execution_id, issue_id, agent_run_id, seq, type, role, text, tool_name, tool_input, tool_output, tool_call_id, occurred_at, phase, subagent
		FROM transcript_events
		WHERE execution_id = ? AND issue_id = ?
		ORDER BY agent_run_id, seq`,
		executionID, issueID,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: transcript events for issue %s/%s: %w", executionID, issueID, err)
	}
	return scanTranscriptEvents(rows, fmt.Sprintf("storage: transcript events for issue %s/%s", executionID, issueID))
}

func scanTranscriptEvents(rows *sql.Rows, contextMsg string) ([]TranscriptEvent, error) {
	defer func() { _ = rows.Close() }()

	var events []TranscriptEvent
	for rows.Next() {
		var event TranscriptEvent
		if err := rows.Scan(
			&event.ExecutionID, &event.IssueID, &event.AgentRunID, &event.Seq, &event.Type, &event.Role,
			&event.Text, &event.ToolName, &event.ToolInput, &event.ToolOutput, &event.ToolCallID, &event.OccurredAt,
			&event.Phase, &event.Subagent,
		); err != nil {
			return nil, fmt.Errorf("%s: scan transcript event: %w", contextMsg, err)
		}
		event.OccurredAt = event.OccurredAt.UTC()
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", contextMsg, err)
	}
	return events, nil
}
