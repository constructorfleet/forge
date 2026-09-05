package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Teagan42/forge/internal/domain"
)

// appendTransitionEvent records an "issue.transitioned" Event with a
// JSON-encoded {"from":...,"to":...} payload. json.Marshal is used rather
// than hand-built %q formatting because %q produces Go string escaping, not
// JSON escaping — the two coincide for plain ASCII state names but would
// diverge for arbitrary caller-supplied strings.
func appendTransitionEvent(ctx context.Context, tx *sql.Tx, executionID, issueID string, from, to domain.IssueState) error {
	data, err := json.Marshal(struct {
		From string `json:"from"`
		To   string `json:"to"`
	}{From: string(from), To: string(to)})
	if err != nil {
		return err
	}
	return insertEvent(ctx, tx, Event{
		ExecutionID: executionID,
		IssueID:     issueID,
		Type:        "issue.transitioned",
		Data:        string(data),
		OccurredAt:  time.Now().UTC(),
	})
}

// appendClaimEvent records an "issue.claimed" Event with a JSON-encoded
// {"worker_ref":...} payload. worker_ref is caller-supplied, so it is
// JSON-marshaled rather than interpolated.
func appendClaimEvent(ctx context.Context, tx *sql.Tx, executionID, issueID, workerRef string, occurredAt time.Time) error {
	data, err := json.Marshal(struct {
		WorkerRef string `json:"worker_ref"`
	}{WorkerRef: workerRef})
	if err != nil {
		return err
	}
	return insertEvent(ctx, tx, Event{
		ExecutionID: executionID,
		IssueID:     issueID,
		Type:        "issue.claimed",
		Data:        string(data),
		OccurredAt:  occurredAt,
	})
}

// MarshalEvent builds an Event for executionID/eventType at occurredAt,
// JSON-encoding payload, so callers across packages (internal/planengine,
// internal/wayfinding) share one marshal-then-construct step instead of each
// duplicating it.
func MarshalEvent(executionID, eventType string, occurredAt time.Time, payload any) (Event, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("marshal %s event payload: %w", eventType, err)
	}
	return Event{
		ExecutionID: executionID,
		Type:        eventType,
		Data:        string(data),
		OccurredAt:  occurredAt,
	}, nil
}

// AppendEvent records a standalone Event.
func (s *SQLiteStore) AppendEvent(ctx context.Context, event Event) error {
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	if err := insertEvent(ctx, s.db, event); err != nil {
		return fmt.Errorf("storage: append event: %w", err)
	}
	return nil
}

func insertEvent(ctx context.Context, q querier, event Event) error {
	var issueID any
	if event.IssueID != "" {
		issueID = event.IssueID
	}
	_, err := q.ExecContext(ctx, `
		INSERT INTO events (execution_id, issue_id, type, data, occurred_at)
		VALUES (?, ?, ?, ?, ?)`,
		event.ExecutionID, issueID, event.Type, event.Data, event.OccurredAt.UTC(),
	)
	return err
}

// EventsByExecution returns every Event recorded for an Execution, ordered
// by occurrence time.
func (s *SQLiteStore) EventsByExecution(ctx context.Context, executionID string) ([]Event, error) {
	events, err := queryEvents(ctx, s.db, "execution_id = ?", executionID)
	if err != nil {
		return nil, fmt.Errorf("storage: events for execution %s: %w", executionID, err)
	}
	return events, nil
}

// EventsByIssue returns every Event recorded for one Issue within an
// Execution, ordered by occurrence time.
func (s *SQLiteStore) EventsByIssue(ctx context.Context, executionID, issueID string) ([]Event, error) {
	events, err := queryEvents(ctx, s.db, "execution_id = ? AND issue_id = ?", executionID, issueID)
	if err != nil {
		return nil, fmt.Errorf("storage: events for issue %s/%s: %w", executionID, issueID, err)
	}
	return events, nil
}

// EventsByTimeRange returns every Event recorded for an Execution whose
// OccurredAt falls within [from, to], ordered by occurrence time.
func (s *SQLiteStore) EventsByTimeRange(ctx context.Context, executionID string, from, to time.Time) ([]Event, error) {
	events, err := queryEvents(ctx, s.db, "execution_id = ? AND occurred_at BETWEEN ? AND ?", executionID, from.UTC(), to.UTC())
	if err != nil {
		return nil, fmt.Errorf("storage: events for execution %s in range: %w", executionID, err)
	}
	return events, nil
}

// queryEvents runs the shared events SELECT with the given WHERE clause and
// args, ordered by occurrence time. EventsByExecution/EventsByIssue/
// EventsByTimeRange differ only in `where`; this is the one place that
// owns the SELECT, ORDER BY, and row scan.
func queryEvents(ctx context.Context, q querier, where string, args ...any) ([]Event, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT id, execution_id, COALESCE(issue_id, ''), type, data, occurred_at
		FROM events WHERE `+where+`
		ORDER BY occurred_at, id`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	return scanEvents(rows)
}

func scanEvents(rows *sql.Rows) ([]Event, error) {
	defer func() { _ = rows.Close() }()

	var events []Event
	for rows.Next() {
		var (
			e          Event
			occurredAt time.Time
		)
		if err := rows.Scan(&e.ID, &e.ExecutionID, &e.IssueID, &e.Type, &e.Data, &occurredAt); err != nil {
			return nil, fmt.Errorf("storage: scan event: %w", err)
		}
		e.OccurredAt = occurredAt.UTC()
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: scan events: %w", err)
	}
	return events, nil
}
