package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Teagan42/forge/internal/domain"
)

// RecordWorkspace persists the Workspace path/branch for one Issue,
// replacing any earlier record for that same Execution/Issue pair.
func (s *SQLiteStore) RecordWorkspace(ctx context.Context, executionID string, ws domain.Workspace) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO workspaces (execution_id, issue_id, path, branch)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(execution_id, issue_id) DO UPDATE SET
			path = excluded.path,
			branch = excluded.branch`,
		executionID, ws.IssueID, ws.Path, ws.Branch,
	)
	if err != nil {
		return fmt.Errorf("storage: record workspace for issue %s/%s: %w", executionID, ws.IssueID, err)
	}
	return nil
}

// WorkspaceByIssue reloads the persisted Workspace for one Issue.
func (s *SQLiteStore) WorkspaceByIssue(ctx context.Context, executionID, issueID string) (domain.Workspace, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT issue_id, path, branch
		FROM workspaces
		WHERE execution_id = ? AND issue_id = ?`,
		executionID, issueID,
	)
	var ws domain.Workspace
	if err := row.Scan(&ws.IssueID, &ws.Path, &ws.Branch); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Workspace{}, fmt.Errorf("storage: workspace %s/%s: %w", executionID, issueID, ErrNotFound)
		}
		return domain.Workspace{}, fmt.Errorf("storage: load workspace %s/%s: %w", executionID, issueID, err)
	}
	return ws, nil
}
