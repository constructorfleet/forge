package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Teagan42/forge/internal/domain"
)

// CreateIssue persists a new Issue, its Dependencies, and its RetryBudget.
func (s *SQLiteStore) CreateIssue(ctx context.Context, issue domain.Issue) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: create issue %s: %w", issue.ID, err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := insertIssue(ctx, tx, issue); err != nil {
		return fmt.Errorf("storage: create issue %s: %w", issue.ID, err)
	}
	for _, dep := range issue.Dependencies {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO dependencies (execution_id, issue_id, depends_on_id)
			VALUES (?, ?, ?)`,
			issue.ExecutionID, dep.IssueID, dep.DependsOnID,
		); err != nil {
			return fmt.Errorf("storage: create issue %s dependency: %w", issue.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: create issue %s: %w", issue.ID, err)
	}
	return nil
}

func insertIssue(ctx context.Context, tx *sql.Tx, issue domain.Issue) error {
	limits := issue.RetryBudget.Limits()
	_, err := tx.ExecContext(ctx, `
		INSERT INTO execution_issues (
			execution_id, issue_id, state, scope,
			retry_gate_limit, retry_gate_used,
			retry_review_limit, retry_review_used,
			retry_ci_limit, retry_ci_used
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		issue.ExecutionID, issue.ID, string(issue.State), string(issue.Scope),
		limits.Gate, issue.RetryBudget.GateFailures(),
		limits.Review, issue.RetryBudget.ReviewFailures(),
		limits.CI, issue.RetryBudget.CIFailures(),
	)
	return err
}

// GetIssue reloads a single Issue by Execution and Issue ID.
func (s *SQLiteStore) GetIssue(ctx context.Context, executionID, issueID string) (domain.Issue, error) {
	return s.getIssue(ctx, s.db, executionID, issueID)
}

func (s *SQLiteStore) getIssue(ctx context.Context, q querier, executionID, issueID string) (domain.Issue, error) {
	row := q.QueryRowContext(ctx, `
		SELECT issue_id, state, scope,
			retry_gate_limit, retry_gate_used,
			retry_review_limit, retry_review_used,
			retry_ci_limit, retry_ci_used
		FROM execution_issues WHERE execution_id = ? AND issue_id = ?`,
		executionID, issueID,
	)

	issue, err := scanIssueRow(row, executionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Issue{}, fmt.Errorf("storage: issue %s/%s: %w", executionID, issueID, ErrNotFound)
		}
		return domain.Issue{}, fmt.Errorf("storage: load issue %s/%s: %w", executionID, issueID, err)
	}

	deps, err := loadDependencies(ctx, q, executionID, issueID)
	if err != nil {
		return domain.Issue{}, fmt.Errorf("storage: load issue %s/%s dependencies: %w", executionID, issueID, err)
	}
	issue.Dependencies = deps
	return issue, nil
}

// scanIssueRow scans one execution_issues row into a domain.Issue. It
// accepts the scanner interface (satisfied by both *sql.Row and *sql.Rows)
// so single-row lookups (getIssue) and bulk listing (ListIssues) share one
// scan implementation.
func scanIssueRow(row scanner, executionID string) (domain.Issue, error) {
	var (
		state                                                         string
		scope                                                         string
		gateLimit, gateUsed, reviewLimit, reviewUsed, ciLimit, ciUsed int
	)
	issue := domain.Issue{ExecutionID: executionID}
	if err := row.Scan(
		&issue.ID, &state, &scope,
		&gateLimit, &gateUsed,
		&reviewLimit, &reviewUsed,
		&ciLimit, &ciUsed,
	); err != nil {
		return domain.Issue{}, err
	}
	issue.State = domain.IssueState(state)
	issue.Scope = domain.IssueScope(scope)
	issue.RetryBudget = domain.NewRetryBudgetFrom(
		domain.RetryLimits{Gate: gateLimit, Review: reviewLimit, CI: ciLimit},
		gateUsed, reviewUsed, ciUsed,
	)
	return issue, nil
}

func loadDependencies(ctx context.Context, q querier, executionID, issueID string) ([]domain.Dependency, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT depends_on_id FROM dependencies
		WHERE execution_id = ? AND issue_id = ?
		ORDER BY depends_on_id`,
		executionID, issueID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var deps []domain.Dependency
	for rows.Next() {
		var dependsOn string
		if err := rows.Scan(&dependsOn); err != nil {
			return nil, err
		}
		deps = append(deps, domain.Dependency{IssueID: issueID, DependsOnID: dependsOn})
	}
	return deps, rows.Err()
}

// ListIssues reloads every Issue recorded against an Execution. It issues
// exactly two queries regardless of Issue count — one for the Issues
// themselves, one for every Dependency row across all of them — rather than
// the N+1 round-trips a per-Issue reload would cost.
func (s *SQLiteStore) ListIssues(ctx context.Context, executionID string) ([]domain.Issue, error) {
	issues, order, err := listIssueRows(ctx, s.db, executionID)
	if err != nil {
		return nil, fmt.Errorf("storage: list issues for execution %s: %w", executionID, err)
	}

	deps, err := listDependencyRows(ctx, s.db, executionID)
	if err != nil {
		return nil, fmt.Errorf("storage: list issues for execution %s: %w", executionID, err)
	}
	for issueID, issueDeps := range deps {
		issue := issues[issueID]
		issue.Dependencies = issueDeps
		issues[issueID] = issue
	}

	result := make([]domain.Issue, 0, len(order))
	for _, id := range order {
		result = append(result, issues[id])
	}
	return result, nil
}

func listIssueRows(ctx context.Context, q querier, executionID string) (map[string]domain.Issue, []string, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT issue_id, state, scope,
			retry_gate_limit, retry_gate_used,
			retry_review_limit, retry_review_used,
			retry_ci_limit, retry_ci_used
		FROM execution_issues
		WHERE execution_id = ?
		ORDER BY issue_id`,
		executionID,
	)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()

	issues := make(map[string]domain.Issue)
	var order []string
	for rows.Next() {
		issue, err := scanIssueRow(rows, executionID)
		if err != nil {
			return nil, nil, err
		}
		issues[issue.ID] = issue
		order = append(order, issue.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return issues, order, nil
}

func listDependencyRows(ctx context.Context, q querier, executionID string) (map[string][]domain.Dependency, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT issue_id, depends_on_id FROM dependencies
		WHERE execution_id = ?
		ORDER BY issue_id, depends_on_id`,
		executionID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	deps := make(map[string][]domain.Dependency)
	for rows.Next() {
		var issueID, dependsOn string
		if err := rows.Scan(&issueID, &dependsOn); err != nil {
			return nil, err
		}
		deps[issueID] = append(deps[issueID], domain.Dependency{IssueID: issueID, DependsOnID: dependsOn})
	}
	return deps, rows.Err()
}

// TransitionIssue moves an Issue to a new state and appends a corresponding
// Event, both inside a single database transaction.
func (s *SQLiteStore) TransitionIssue(ctx context.Context, executionID, issueID string, to domain.IssueState) (domain.Issue, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Issue{}, fmt.Errorf("storage: transition issue %s/%s: %w", executionID, issueID, err)
	}
	defer func() { _ = tx.Rollback() }()

	issue, err := s.getIssue(ctx, tx, executionID, issueID)
	if err != nil {
		return domain.Issue{}, err
	}

	from := issue.State
	if err := issue.ApplyTransition(to); err != nil {
		// Illegal transition: propagate domain's own error, no write.
		return domain.Issue{}, err
	}

	affected, err := updateIssueStateCAS(ctx, tx, executionID, issueID, string(from), string(issue.State))
	if err != nil {
		return domain.Issue{}, fmt.Errorf("storage: transition issue %s/%s: %w", executionID, issueID, err)
	}
	if affected == 0 {
		// The row's state no longer matches what we just read it as within
		// this same transaction — something else changed it concurrently.
		return domain.Issue{}, fmt.Errorf("storage: transition issue %s/%s: %w", executionID, issueID, ErrConcurrentModification)
	}

	if err := appendTransitionEvent(ctx, tx, executionID, issueID, from, issue.State); err != nil {
		return domain.Issue{}, fmt.Errorf("storage: transition issue %s/%s: %w", executionID, issueID, err)
	}

	if err := tx.Commit(); err != nil {
		return domain.Issue{}, fmt.Errorf("storage: transition issue %s/%s: %w", executionID, issueID, err)
	}
	return issue, nil
}

// UpdateRetryBudget persists budget's used-counters for issueID within
// executionID. See Store.UpdateRetryBudget's doc comment for why the
// repair loop needs this: TransitionIssue always reloads the Issue fresh
// from execution_issues, so an in-memory-only RecordGateFailure/
// RecordReviewRejection/RecordCIFailure would otherwise be silently
// discarded on the very next transition.
func (s *SQLiteStore) UpdateRetryBudget(ctx context.Context, executionID, issueID string, budget domain.RetryBudget) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE execution_issues
		SET retry_gate_used = ?, retry_review_used = ?, retry_ci_used = ?
		WHERE execution_id = ? AND issue_id = ?`,
		budget.GateFailures(), budget.ReviewFailures(), budget.CIFailures(),
		executionID, issueID,
	)
	if err != nil {
		return fmt.Errorf("storage: update retry budget for issue %s/%s: %w", executionID, issueID, err)
	}
	return nil
}

// updateIssueStateCAS updates an Issue's state only if it still matches
// `from`, returning the number of rows affected (0 or 1). This is a
// compare-and-swap guard on top of the transactional isolation the current
// single-connection SQLite pool already provides — cheap defense-in-depth
// against a future multi-connection or Postgres implementation where a
// naive UPDATE ... WHERE execution_id = ? AND issue_id = ? could silently
// overwrite a state that changed between the read and the write.
func updateIssueStateCAS(ctx context.Context, tx *sql.Tx, executionID, issueID, from, to string) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE execution_issues SET state = ?
		WHERE execution_id = ? AND issue_id = ? AND state = ?`,
		to, executionID, issueID, from,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
