package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// RecordReviewRun persists one Review invocation's outcome and its
// Findings, and appends a "review.run" Event, all inside a single database
// transaction — mirroring RecordGateRun's shape for the Gate Runner.
func (s *SQLiteStore) RecordReviewRun(ctx context.Context, run ReviewRun) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: record review run for issue %s/%s: %w", run.ExecutionID, run.IssueID, err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO review_runs (execution_id, issue_id, verdict, summary, diff, started_at, finished_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		run.ExecutionID, run.IssueID, run.Verdict, run.Summary, run.Diff,
		run.StartedAt.UTC(), run.FinishedAt.UTC(),
	)
	if err != nil {
		switch {
		case isForeignKeyConstraintErr(err):
			return fmt.Errorf("storage: record review run for issue %s/%s: %w", run.ExecutionID, run.IssueID, ErrNotFound)
		default:
			return fmt.Errorf("storage: record review run for issue %s/%s: %w", run.ExecutionID, run.IssueID, err)
		}
	}

	reviewRunID, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("storage: record review run for issue %s/%s: %w", run.ExecutionID, run.IssueID, err)
	}

	for _, finding := range run.Findings {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO review_findings (review_run_id, severity, file, line, message)
			VALUES (?, ?, ?, ?, ?)`,
			reviewRunID, finding.Severity, finding.File, finding.Line, finding.Message,
		); err != nil {
			return fmt.Errorf("storage: record review run for issue %s/%s: insert finding: %w", run.ExecutionID, run.IssueID, err)
		}
	}

	for _, env := range run.Envelopes {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO review_axis_envelopes (review_run_id, axis, ran, reason, input_tokens, output_tokens, raw_findings)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			reviewRunID, env.Axis, env.Ran, env.Reason, env.InputTokens, env.OutputTokens, env.RawFindings,
		); err != nil {
			return fmt.Errorf("storage: record review run for issue %s/%s: insert axis envelope: %w", run.ExecutionID, run.IssueID, err)
		}
	}

	if err := appendReviewRunEvent(ctx, tx, run); err != nil {
		return fmt.Errorf("storage: record review run for issue %s/%s: %w", run.ExecutionID, run.IssueID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: record review run for issue %s/%s: %w", run.ExecutionID, run.IssueID, err)
	}
	return nil
}

// appendReviewRunEvent records a "review.run" Event with a JSON-encoded
// summary of run. The diff and per-Finding detail live in review_runs/
// review_findings, not duplicated into the event log — the event carries
// only what's needed to scan the audit trail (verdict, summary, finding
// count), matching appendGateRunEvent's convention.
func appendReviewRunEvent(ctx context.Context, tx *sql.Tx, run ReviewRun) error {
	data, err := json.Marshal(struct {
		Verdict      string `json:"verdict"`
		Summary      string `json:"summary"`
		FindingCount int    `json:"finding_count"`
	}{Verdict: run.Verdict, Summary: run.Summary, FindingCount: len(run.Findings)})
	if err != nil {
		return err
	}
	return insertEvent(ctx, tx, Event{
		ExecutionID: run.ExecutionID,
		IssueID:     run.IssueID,
		Type:        "review.run",
		Data:        string(data),
		OccurredAt:  run.FinishedAt,
	})
}

// ReviewRunsByIssue returns every ReviewRun recorded for one Issue within an
// Execution, ordered by insertion, each with its Findings populated.
// ReviewRunsByIssue issues one findings query per ReviewRun (N+1) rather
// than a single join. Acceptable for now: an Issue accumulates at most a
// handful of ReviewRuns (one per Review invocation, bounded by the review
// retry ceiling — CONTEXT.md "Retry Budget"), not an unbounded collection,
// so this stays a small, fixed number of round trips per call rather than
// scaling with data volume. Revisit with a single review_runs/
// review_findings join (grouping rows as they're scanned, ordered by run
// id then finding id) if that assumption stops holding.
func (s *SQLiteStore) ReviewRunsByIssue(ctx context.Context, executionID, issueID string) ([]ReviewRun, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, execution_id, issue_id, verdict, summary, diff, started_at, finished_at
		FROM review_runs
		WHERE execution_id = ? AND issue_id = ?
		ORDER BY id`,
		executionID, issueID,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: review runs for issue %s/%s: %w", executionID, issueID, err)
	}
	defer func() { _ = rows.Close() }()

	type indexedRun struct {
		id  int64
		run ReviewRun
	}
	var runs []indexedRun
	for rows.Next() {
		var (
			id  int64
			run ReviewRun
		)
		if err := rows.Scan(
			&id, &run.ExecutionID, &run.IssueID, &run.Verdict, &run.Summary, &run.Diff,
			&run.StartedAt, &run.FinishedAt,
		); err != nil {
			return nil, fmt.Errorf("storage: scan review run: %w", err)
		}
		run.StartedAt = run.StartedAt.UTC()
		run.FinishedAt = run.FinishedAt.UTC()
		runs = append(runs, indexedRun{id: id, run: run})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: review runs for issue %s/%s: %w", executionID, issueID, err)
	}

	out := make([]ReviewRun, len(runs))
	for i, ir := range runs {
		findings, err := s.reviewFindingsByRun(ctx, ir.id)
		if err != nil {
			return nil, fmt.Errorf("storage: review runs for issue %s/%s: %w", executionID, issueID, err)
		}
		ir.run.Findings = findings

		envelopes, err := s.reviewAxisEnvelopesByRun(ctx, ir.id)
		if err != nil {
			return nil, fmt.Errorf("storage: review runs for issue %s/%s: %w", executionID, issueID, err)
		}
		ir.run.Envelopes = envelopes

		out[i] = ir.run
	}
	return out, nil
}

func (s *SQLiteStore) reviewFindingsByRun(ctx context.Context, reviewRunID int64) ([]ReviewFinding, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT severity, file, line, message
		FROM review_findings
		WHERE review_run_id = ?
		ORDER BY id`,
		reviewRunID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var findings []ReviewFinding
	for rows.Next() {
		var f ReviewFinding
		if err := rows.Scan(&f.Severity, &f.File, &f.Line, &f.Message); err != nil {
			return nil, fmt.Errorf("scan review finding: %w", err)
		}
		findings = append(findings, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return findings, nil
}

// reviewAxisEnvelopesByRun returns every ReviewAxisEnvelope recorded for one
// ReviewRun (issue #162), ordered by insertion — the same one-row-per-axis
// order Reviewer.Review's fan-out wrote them in.
func (s *SQLiteStore) reviewAxisEnvelopesByRun(ctx context.Context, reviewRunID int64) ([]ReviewAxisEnvelope, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT axis, ran, reason, input_tokens, output_tokens, raw_findings
		FROM review_axis_envelopes
		WHERE review_run_id = ?
		ORDER BY id`,
		reviewRunID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var envelopes []ReviewAxisEnvelope
	for rows.Next() {
		var (
			e           ReviewAxisEnvelope
			inputTokens sql.NullInt64
			outTokens   sql.NullInt64
		)
		if err := rows.Scan(&e.Axis, &e.Ran, &e.Reason, &inputTokens, &outTokens, &e.RawFindings); err != nil {
			return nil, fmt.Errorf("scan review axis envelope: %w", err)
		}
		if inputTokens.Valid {
			v := int(inputTokens.Int64)
			e.InputTokens = &v
		}
		if outTokens.Valid {
			v := int(outTokens.Int64)
			e.OutputTokens = &v
		}
		envelopes = append(envelopes, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return envelopes, nil
}
