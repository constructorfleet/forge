package storage

import (
	"context"
	"database/sql"
	"fmt"
)

// MaxTranscriptEventsPerRun caps how many transcript_events rows one AgentRun
// may retain (ADR 0030). The cap is enforced in SQL on the append path:
// inserting past it deletes the oldest seqs in the same transaction, leaving
// seq gaps. Mirrors the in-memory recorder window but bounds reattach
// backfill at the store.
const MaxTranscriptEventsPerRun = 5000

// RecordTranscriptEvents appends every event captured during one AgentRun
// (agentRunID, as returned by RecordAgentRun), in a single transaction,
// enforcing MaxTranscriptEventsPerRun by deleting the oldest rows in the
// same transaction as the append. TRUNCATION-marker events are filtered here
// so storage never persists them (ADR 0030); a reader sees seq gaps instead.
// A no-op when events is empty.
func (s *SQLiteStore) RecordTranscriptEvents(ctx context.Context, executionID, issueID string, agentRunID int64, events []TranscriptEvent) error {
	events = filterTruncationEvents(events)
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
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM transcript_events
		WHERE agent_run_id = ?
		  AND id NOT IN (
			SELECT id FROM transcript_events
			WHERE agent_run_id = ?
			ORDER BY seq DESC
			LIMIT ?)`,
		agentRunID, agentRunID, MaxTranscriptEventsPerRun,
	); err != nil {
		return fmt.Errorf("storage: enforce transcript retention for issue %s/%s: %w", executionID, issueID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: record transcript events for issue %s/%s: %w", executionID, issueID, err)
	}
	return nil
}

// filterTruncationEvents drops synthetic TRUNCATION-marker events so they are
// never persisted (ADR 0030).
func filterTruncationEvents(events []TranscriptEvent) []TranscriptEvent {
	out := events[:0]
	for _, event := range events {
		if event.Type == "TRUNCATION" {
			continue
		}
		out = append(out, event)
	}
	return out
}

// insertTranscriptEvents inserts events for one AgentRun using tx. An empty
// slice inserts nothing.
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

// TranscriptEventsAfter returns up to limit TranscriptEvents recorded for one
// AgentRun whose Seq is strictly greater than afterSeq, in Seq order — the
// bounded tail API a live reader polls to follow a run (ADR 0030). A cursor
// into an eviction gap still reads correctly: eviction only removes old seqs,
// never the ordering, so afterSeq can point at a seq that no longer exists.
func (s *SQLiteStore) TranscriptEventsAfter(ctx context.Context, agentRunID, afterSeq, limit int64) ([]TranscriptEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT execution_id, issue_id, agent_run_id, seq, type, role, text, tool_name, tool_input, tool_output, tool_call_id, occurred_at, phase, subagent
		FROM transcript_events
		WHERE agent_run_id = ? AND seq > ?
		ORDER BY seq
		LIMIT ?`,
		agentRunID, afterSeq, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: transcript events after %d for run %d: %w", afterSeq, agentRunID, err)
	}
	return scanTranscriptEvents(rows, fmt.Sprintf("storage: transcript events after %d for run %d", afterSeq, agentRunID))
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
