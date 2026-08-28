package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// RecordPullRequest persists one created (or recovered) pull request and
// appends a "pull_request.created" Event, all inside a single database
// transaction — mirroring RecordGateRun/RecordReviewRun's shape.
func (s *SQLiteStore) RecordPullRequest(ctx context.Context, pr PullRequest) error {
	existing, err := s.PullRequestsByIssue(ctx, pr.ExecutionID, pr.IssueID)
	if err != nil {
		return err
	}
	for _, prior := range existing {
		if prior.Number == pr.Number && prior.URL == pr.URL && prior.CommitSHA == pr.CommitSHA {
			return nil
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: record pull request for issue %s/%s: %w", pr.ExecutionID, pr.IssueID, err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO pull_requests (execution_id, issue_id, url, number, commit_sha, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		pr.ExecutionID, pr.IssueID, pr.URL, pr.Number, pr.CommitSHA, pr.CreatedAt.UTC(),
	)
	if err != nil {
		switch {
		case isForeignKeyConstraintErr(err):
			return fmt.Errorf("storage: record pull request for issue %s/%s: %w", pr.ExecutionID, pr.IssueID, ErrNotFound)
		default:
			return fmt.Errorf("storage: record pull request for issue %s/%s: %w", pr.ExecutionID, pr.IssueID, err)
		}
	}

	if err := appendPullRequestEvent(ctx, tx, pr); err != nil {
		return fmt.Errorf("storage: record pull request for issue %s/%s: %w", pr.ExecutionID, pr.IssueID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: record pull request for issue %s/%s: %w", pr.ExecutionID, pr.IssueID, err)
	}
	return nil
}

// appendPullRequestEvent records a "pull_request.created" Event with a
// JSON-encoded summary of pr — the full record lives in pull_requests, so
// the event carries only what's needed to scan the audit trail.
func appendPullRequestEvent(ctx context.Context, tx *sql.Tx, pr PullRequest) error {
	data, err := json.Marshal(struct {
		URL       string `json:"url"`
		Number    int    `json:"number"`
		CommitSHA string `json:"commit_sha"`
	}{URL: pr.URL, Number: pr.Number, CommitSHA: pr.CommitSHA})
	if err != nil {
		return err
	}
	return insertEvent(ctx, tx, Event{
		ExecutionID: pr.ExecutionID,
		IssueID:     pr.IssueID,
		Type:        "pull_request.created",
		Data:        string(data),
		OccurredAt:  pr.CreatedAt,
	})
}

// PullRequestsByIssue returns every PullRequest recorded for one Issue
// within an Execution, ordered by insertion.
func (s *SQLiteStore) PullRequestsByIssue(ctx context.Context, executionID, issueID string) ([]PullRequest, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT execution_id, issue_id, url, number, commit_sha, created_at
		FROM pull_requests
		WHERE execution_id = ? AND issue_id = ?
		ORDER BY id`,
		executionID, issueID,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: pull requests for issue %s/%s: %w", executionID, issueID, err)
	}
	defer func() { _ = rows.Close() }()

	var prs []PullRequest
	for rows.Next() {
		var pr PullRequest
		if err := rows.Scan(&pr.ExecutionID, &pr.IssueID, &pr.URL, &pr.Number, &pr.CommitSHA, &pr.CreatedAt); err != nil {
			return nil, fmt.Errorf("storage: scan pull request: %w", err)
		}
		pr.CreatedAt = pr.CreatedAt.UTC()
		prs = append(prs, pr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: pull requests for issue %s/%s: %w", executionID, issueID, err)
	}
	return prs, nil
}
