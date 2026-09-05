package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
			INSERT INTO review_axis_envelopes (review_run_id, axis, ran, reason, input_tokens, output_tokens, raw_envelope)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			reviewRunID, env.Axis, env.Ran, env.Reason, env.InputTokens, env.OutputTokens, env.RawEnvelope,
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

// ReviewRunsByIssueWithoutDiff returns every ReviewRun recorded for one
// Issue within an Execution, ordered by insertion, each with its Findings
// and Envelopes populated and its Diff left empty. Callers that need one
// run's diff read it separately with LatestReviewDiff.
func (s *SQLiteStore) ReviewRunsByIssueWithoutDiff(ctx context.Context, executionID, issueID string) ([]ReviewRun, error) {
	runs, order, err := s.reviewRunsWithFindings(ctx, executionID, issueID)
	if err != nil {
		return nil, fmt.Errorf("storage: review runs for issue %s/%s: %w", executionID, issueID, err)
	}
	if err := s.attachReviewAxisEnvelopes(ctx, executionID, issueID, runs); err != nil {
		return nil, fmt.Errorf("storage: review runs for issue %s/%s: %w", executionID, issueID, err)
	}

	out := make([]ReviewRun, len(order))
	for i, id := range order {
		out[i] = runs[id]
	}
	return out, nil
}

// reviewRunsWithFindings runs the review_runs/review_findings join and
// returns each ReviewRun keyed by its row id, plus the ids in run order, so
// the caller can attach Envelopes and then flatten to a slice. review_runs
// is the left side of the join, so a ReviewRun with no Findings still
// yields exactly one row, via a NULL right side, rather than disappearing
// from the result.
func (s *SQLiteStore) reviewRunsWithFindings(ctx context.Context, executionID, issueID string) (map[int64]ReviewRun, []int64, error) {
	rows, err := s.db.QueryContext(ctx, reviewRunsFindingsQuery, executionID, issueID)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()

	runs := map[int64]ReviewRun{}
	var order []int64
	for rows.Next() {
		var (
			id             int64
			run            ReviewRun
			sev, file, msg sql.NullString
			line           sql.NullInt64
		)
		if err := rows.Scan(
			&id, &run.ExecutionID, &run.IssueID, &run.Verdict, &run.Summary,
			&run.StartedAt, &run.FinishedAt, &sev, &file, &line, &msg,
		); err != nil {
			return nil, nil, fmt.Errorf("scan review run: %w", err)
		}

		existing, seen := runs[id]
		if !seen {
			run.StartedAt = run.StartedAt.UTC()
			run.FinishedAt = run.FinishedAt.UTC()
			existing = run
			order = append(order, id)
		}
		if sev.Valid {
			existing.Findings = append(existing.Findings, ReviewFinding{
				Severity: sev.String, File: file.String, Line: int(line.Int64), Message: msg.String,
			})
		}
		runs[id] = existing
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return runs, order, nil
}

// attachReviewAxisEnvelopes runs the review_runs/review_axis_envelopes join
// and appends each row's ReviewAxisEnvelope onto its ReviewRun in runs, in
// the axis order the rows were originally inserted in.
func (s *SQLiteStore) attachReviewAxisEnvelopes(ctx context.Context, executionID, issueID string, runs map[int64]ReviewRun) error {
	rows, err := s.db.QueryContext(ctx, reviewRunsEnvelopesQuery, executionID, issueID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			id          int64
			axis        sql.NullString
			ran         sql.NullBool
			reason      sql.NullString
			inputTokens sql.NullInt64
			outTokens   sql.NullInt64
			rawEnvelope sql.NullString
		)
		if err := rows.Scan(&id, &axis, &ran, &reason, &inputTokens, &outTokens, &rawEnvelope); err != nil {
			return fmt.Errorf("scan review axis envelope: %w", err)
		}
		if !axis.Valid {
			continue
		}
		run, ok := runs[id]
		if !ok {
			continue
		}
		env := ReviewAxisEnvelope{Axis: axis.String, Ran: ran.Bool, Reason: reason.String, RawEnvelope: rawEnvelope.String}
		if inputTokens.Valid {
			v := int(inputTokens.Int64)
			env.InputTokens = &v
		}
		if outTokens.Valid {
			v := int(outTokens.Int64)
			env.OutputTokens = &v
		}
		run.Envelopes = append(run.Envelopes, env)
		runs[id] = run
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return nil
}

const reviewRunsFindingsQuery = `
	SELECT r.id, r.execution_id, r.issue_id, r.verdict, r.summary, r.started_at, r.finished_at,
	       f.severity, f.file, f.line, f.message
	FROM review_runs r
	LEFT JOIN review_findings f ON f.review_run_id = r.id
	WHERE r.execution_id = ? AND r.issue_id = ?
	ORDER BY r.id, f.id`

const reviewRunsEnvelopesQuery = `
	SELECT r.id, e.axis, e.ran, e.reason, e.input_tokens, e.output_tokens, e.raw_envelope
	FROM review_runs r
	LEFT JOIN review_axis_envelopes e ON e.review_run_id = r.id
	WHERE r.execution_id = ? AND r.issue_id = ?
	ORDER BY r.id, e.id`

// LatestReviewOutcome returns the current Review verdict for one Issue and
// whether that run stored a diff. It reads one row and returns no diff body, so
// a per-second poll costs the same whatever the review history holds. Use
// ReviewRunsByIssueWithoutDiff where the caller needs the runs themselves,
// or LatestReviewDiff where it needs the diff body.
func (s *SQLiteStore) LatestReviewOutcome(ctx context.Context, executionID, issueID string) (ReviewOutcome, error) {
	var out ReviewOutcome
	err := s.db.QueryRowContext(ctx, `
		SELECT verdict, LENGTH(diff) > 0
		FROM review_runs
		WHERE execution_id = ? AND issue_id = ?
		ORDER BY id DESC
		LIMIT 1`,
		executionID, issueID,
	).Scan(&out.Verdict, &out.HasDiff)
	if errors.Is(err, sql.ErrNoRows) {
		return ReviewOutcome{}, nil
	}
	if err != nil {
		return ReviewOutcome{}, fmt.Errorf("storage: latest review outcome for issue %s/%s: %w", executionID, issueID, err)
	}
	out.Recorded = true
	return out, nil
}

// LatestReviewDiff returns the current Review run's stored diff for one Issue.
// It reads the one diff column alone, with no findings and no axis envelopes, so
// the on-request pager read never loads the whole review history. An Issue with
// no Review run, or a run that stored an empty diff, returns "".
//
// It repeats LatestReviewOutcome's "highest id wins" rule, so the strip's
// verdict and the pager's diff always name one run.
func (s *SQLiteStore) LatestReviewDiff(ctx context.Context, executionID, issueID string) (string, error) {
	var diff string
	err := s.db.QueryRowContext(ctx, `
		SELECT diff
		FROM review_runs
		WHERE execution_id = ? AND issue_id = ?
		ORDER BY id DESC
		LIMIT 1`,
		executionID, issueID,
	).Scan(&diff)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("storage: latest review diff for issue %s/%s: %w", executionID, issueID, err)
	}
	return diff, nil
}
