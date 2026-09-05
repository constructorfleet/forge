package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

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
			INSERT INTO dependencies (execution_id, issue_id, depends_on_id, issue_provider, depends_on_provider)
			VALUES (?, ?, ?, ?, ?)`,
			issue.ExecutionID, dep.IssueID, dep.DependsOnID,
			dependencyIssueProvider(issue, dep), dependencyDependsOnProvider(issue, dep),
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
			execution_id, issue_id, provider, title, body, state, scope,
			retry_gate_limit, retry_gate_used,
			retry_review_limit, retry_review_used,
			retry_ci_limit, retry_ci_used,
			retry_provider_limit_limit, retry_provider_limit_used,
			provider_limit_retry_at,
			state_changed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		issue.ExecutionID, issue.ID, issue.Provider, issue.Title, issue.Body, string(issue.State), string(issue.Scope),
		limits.Gate, issue.RetryBudget.GateFailures(),
		limits.Review, issue.RetryBudget.ReviewFailures(),
		limits.CI, issue.RetryBudget.CIFailures(),
		limits.ProviderLimit, issue.RetryBudget.ProviderLimitFailures(),
		issue.ProviderLimitRetryAt,
		nullableTime(issue.StateChangedAt),
	)
	return err
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

func dependencyIssueProvider(issue domain.Issue, dep domain.Dependency) string {
	if dep.IssueRef.Provider != "" {
		return dep.IssueRef.Provider
	}
	return issue.Provider
}

func dependencyDependsOnProvider(issue domain.Issue, dep domain.Dependency) string {
	if dep.DependsOnRef.Provider != "" {
		return dep.DependsOnRef.Provider
	}
	return issue.Provider
}

// GetIssue reloads a single Issue by Execution and Issue ID.
func (s *SQLiteStore) GetIssue(ctx context.Context, executionID, issueID string) (domain.Issue, error) {
	return s.getIssue(ctx, s.db, executionID, issueID)
}

func (s *SQLiteStore) getIssue(ctx context.Context, q querier, executionID, issueID string) (domain.Issue, error) {
	row := q.QueryRowContext(ctx, `
		SELECT issue_id, provider, title, body, state, scope,
			retry_gate_limit, retry_gate_used,
			retry_review_limit, retry_review_used,
			retry_ci_limit, retry_ci_used,
			retry_provider_limit_limit, retry_provider_limit_used,
			provider_limit_retry_at,
			state_changed_at
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
		providerLimitLimit, providerLimitUsed                         int
		providerLimitRetryAt                                          sql.NullTime
		stateChangedAt                                                sql.NullTime
	)
	issue := domain.Issue{ExecutionID: executionID}
	if err := row.Scan(
		&issue.ID, &issue.Provider, &issue.Title, &issue.Body, &state, &scope,
		&gateLimit, &gateUsed,
		&reviewLimit, &reviewUsed,
		&ciLimit, &ciUsed,
		&providerLimitLimit, &providerLimitUsed,
		&providerLimitRetryAt,
		&stateChangedAt,
	); err != nil {
		return domain.Issue{}, err
	}
	issue.State = domain.IssueState(state)
	issue.Scope = domain.IssueScope(scope)
	issue.RetryBudget = domain.NewRetryBudgetFrom(
		domain.RetryLimits{Gate: gateLimit, Review: reviewLimit, CI: ciLimit, ProviderLimit: providerLimitLimit},
		gateUsed, reviewUsed, ciUsed, providerLimitUsed,
	)
	if providerLimitRetryAt.Valid {
		retryAt := providerLimitRetryAt.Time.UTC()
		issue.ProviderLimitRetryAt = &retryAt
	}
	if stateChangedAt.Valid {
		issue.StateChangedAt = stateChangedAt.Time.UTC()
	}
	return issue, nil
}

func loadDependencies(ctx context.Context, q querier, executionID, issueID string) ([]domain.Dependency, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT issue_provider, depends_on_id, depends_on_provider FROM dependencies
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
		var issueProvider, dependsOn, dependsOnProvider string
		if err := rows.Scan(&issueProvider, &dependsOn, &dependsOnProvider); err != nil {
			return nil, err
		}
		deps = append(deps, domain.Dependency{
			IssueID:      issueID,
			DependsOnID:  dependsOn,
			IssueRef:     domain.IssueRef{Provider: issueProvider, ID: issueID},
			DependsOnRef: domain.IssueRef{Provider: dependsOnProvider, ID: dependsOn},
		})
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
		SELECT issue_id, provider, title, body, state, scope,
			retry_gate_limit, retry_gate_used,
			retry_review_limit, retry_review_used,
			retry_ci_limit, retry_ci_used,
			retry_provider_limit_limit, retry_provider_limit_used,
			provider_limit_retry_at,
			state_changed_at
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
		SELECT issue_id, issue_provider, depends_on_id, depends_on_provider FROM dependencies
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
		var issueID, issueProvider, dependsOn, dependsOnProvider string
		if err := rows.Scan(&issueID, &issueProvider, &dependsOn, &dependsOnProvider); err != nil {
			return nil, err
		}
		deps[issueID] = append(deps[issueID], domain.Dependency{
			IssueID:      issueID,
			DependsOnID:  dependsOn,
			IssueRef:     domain.IssueRef{Provider: issueProvider, ID: issueID},
			DependsOnRef: domain.IssueRef{Provider: dependsOnProvider, ID: dependsOn},
		})
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
	issue.StateChangedAt = time.Now().UTC()

	affected, err := updateIssueStateCAS(ctx, tx, executionID, issueID, string(from), string(issue.State), issue.StateChangedAt)
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

// retryClaimFrom and retryClaimTo are the one edge ClaimRetry applies. The
// compare-and-set uses retryClaimFrom as its predicate, so the claim's
// `from` is this state by construction.
const (
	retryClaimFrom = domain.StateFailed
	retryClaimTo   = domain.StateReady
)

// RetryClaim reports one applied retry claim: the Issue as the claim left
// it, and the state the compare-and-set moved it off. Callers use From
// instead of restating the edge, which keeps ClaimRetry the one authority on
// which state a retry claims off.
type RetryClaim struct {
	Issue domain.Issue
	From  domain.IssueState
}

// RetryClaimConflictError reports that ClaimRetry did not find the Issue in
// FAILED. State is the state the transaction read back after its
// compare-and-set matched no row. State alone does not name the cause: a
// rival retry's winner moves on through READY and later states, and an Issue
// that was never FAILED also sits in one of those. Only CANCELLED is
// unambiguous. See engine.retryClaimError, which pairs State with the
// pre-claim state. It wraps
// ErrConcurrentModification, so it still satisfies errors.Is for that
// sentinel.
type RetryClaimConflictError struct {
	ExecutionID string
	IssueID     string
	State       domain.IssueState
}

func (e *RetryClaimConflictError) Error() string {
	return fmt.Sprintf("storage: claim retry %s/%s: issue is %s, want FAILED", e.ExecutionID, e.IssueID, e.State)
}

func (e *RetryClaimConflictError) Unwrap() error { return ErrConcurrentModification }

// ClaimRetry performs one retry claim of a FAILED Issue as a single
// transaction. See engine.RetryIssue for the failure mode separate
// statements produce: the losing retry resets the budget under the winner
// and releases the winner's fresh Worker claim.
//
// The compare-and-set runs first, before any read. A read-then-write
// transaction must upgrade its read snapshot to a write lock, and WAL mode
// refuses that with SQLITE_BUSY_SNAPSHOT when another connection committed
// after the read began — busy_timeout does not wait that out. Two separate
// `forge retry` processes would then get a raw lock error instead of a
// RetryClaimConflictError. Writing first takes the lock up front, and the
// row is read back only to report the state the claim lost to.
func (s *SQLiteStore) ClaimRetry(ctx context.Context, executionID, issueID string, budget domain.RetryBudget) (RetryClaim, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RetryClaim{}, fmt.Errorf("storage: claim retry %s/%s: %w", executionID, issueID, err)
	}
	defer func() { _ = tx.Rollback() }()

	affected, err := updateIssueStateCAS(ctx, tx, executionID, issueID,
		string(retryClaimFrom), string(retryClaimTo), time.Now().UTC())
	if err != nil {
		return RetryClaim{}, fmt.Errorf("storage: claim retry %s/%s: %w", executionID, issueID, err)
	}

	issue, err := s.getIssue(ctx, tx, executionID, issueID)
	if err != nil {
		return RetryClaim{}, err
	}
	if affected == 0 {
		return RetryClaim{}, &RetryClaimConflictError{ExecutionID: executionID, IssueID: issueID, State: issue.State}
	}

	if err := updateRetryBudget(ctx, tx, executionID, issueID, budget); err != nil {
		return RetryClaim{}, fmt.Errorf("storage: claim retry %s/%s: %w", executionID, issueID, err)
	}
	if err := releaseWorkerClaim(ctx, tx, executionID, issueID); err != nil {
		return RetryClaim{}, fmt.Errorf("storage: claim retry %s/%s: %w", executionID, issueID, err)
	}
	if err := appendTransitionEvent(ctx, tx, executionID, issueID, retryClaimFrom, issue.State); err != nil {
		return RetryClaim{}, fmt.Errorf("storage: claim retry %s/%s: %w", executionID, issueID, err)
	}
	if err := tx.Commit(); err != nil {
		return RetryClaim{}, fmt.Errorf("storage: claim retry %s/%s: %w", executionID, issueID, err)
	}
	issue.RetryBudget = budget
	return RetryClaim{Issue: issue, From: retryClaimFrom}, nil
}

// AbortRetry undoes a ClaimRetry the caller could not complete. See
// Store.AbortRetry for why it puts back a state the forward state machine
// forbids, and for the Worker claim it does not restore.
func (s *SQLiteStore) AbortRetry(ctx context.Context, executionID, issueID string, budget domain.RetryBudget) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: abort retry %s/%s: %w", executionID, issueID, err)
	}
	defer func() { _ = tx.Rollback() }()

	affected, err := updateIssueStateCAS(ctx, tx, executionID, issueID,
		string(domain.StateReady), string(domain.StateFailed), time.Now().UTC())
	if err != nil {
		return fmt.Errorf("storage: abort retry %s/%s: %w", executionID, issueID, err)
	}
	if affected == 0 {
		return fmt.Errorf("storage: abort retry %s/%s: %w", executionID, issueID, ErrConcurrentModification)
	}
	if err := updateRetryBudget(ctx, tx, executionID, issueID, budget); err != nil {
		return fmt.Errorf("storage: abort retry %s/%s: %w", executionID, issueID, err)
	}
	if err := appendTransitionEvent(ctx, tx, executionID, issueID, domain.StateReady, domain.StateFailed); err != nil {
		return fmt.Errorf("storage: abort retry %s/%s: %w", executionID, issueID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: abort retry %s/%s: %w", executionID, issueID, err)
	}
	return nil
}

// CancelClaimConflictError reports that ClaimCancel's compare-and-set did
// not find executionID/issueID in the `from` state it was asked to cancel
// off. State is the state the transaction read back after the
// compare-and-set matched no row: another actor moved the Issue there
// between the caller's read and this call. It wraps
// ErrConcurrentModification, so it still satisfies errors.Is for that
// sentinel.
type CancelClaimConflictError struct {
	ExecutionID string
	IssueID     string
	State       domain.IssueState
}

func (e *CancelClaimConflictError) Error() string {
	return fmt.Sprintf("storage: claim cancel %s/%s: issue is %s, not the expected state", e.ExecutionID, e.IssueID, e.State)
}

func (e *CancelClaimConflictError) Unwrap() error { return ErrConcurrentModification }

// ClaimCancel performs one cancel of executionID/issueID as a single
// transaction: it CASes the Issue from `from` to CANCELLED, optionally
// releases its Worker claim, and appends the transition Event, all inside
// one commit. This mirrors ClaimRetry's one-transaction claim (issue 456):
// a cancel that races another writer on the same Issue either wins cleanly
// or loses cleanly with a *CancelClaimConflictError, instead of applying
// the transition and the claim release as two separate statements that
// something else can interleave with (issue 554).
//
// releaseClaim is false when the caller keeps a still-live owner's Worker
// claim (see engine.CancelExecution): the Issue still moves to CANCELLED,
// but the claim is left in place so a second Execution cannot claim the
// same Issue while the live owner still writes to it.
func (s *SQLiteStore) ClaimCancel(ctx context.Context, executionID, issueID string, from domain.IssueState, releaseClaim bool) (domain.Issue, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Issue{}, fmt.Errorf("storage: claim cancel %s/%s: %w", executionID, issueID, err)
	}
	defer func() { _ = tx.Rollback() }()

	affected, err := updateIssueStateCAS(ctx, tx, executionID, issueID,
		string(from), string(domain.StateCancelled), time.Now().UTC())
	if err != nil {
		return domain.Issue{}, fmt.Errorf("storage: claim cancel %s/%s: %w", executionID, issueID, err)
	}

	issue, err := s.getIssue(ctx, tx, executionID, issueID)
	if err != nil {
		return domain.Issue{}, err
	}
	if affected == 0 {
		return domain.Issue{}, &CancelClaimConflictError{ExecutionID: executionID, IssueID: issueID, State: issue.State}
	}

	if releaseClaim {
		if err := releaseWorkerClaim(ctx, tx, executionID, issueID); err != nil {
			return domain.Issue{}, fmt.Errorf("storage: claim cancel %s/%s: %w", executionID, issueID, err)
		}
	}
	if err := appendTransitionEvent(ctx, tx, executionID, issueID, from, domain.StateCancelled); err != nil {
		return domain.Issue{}, fmt.Errorf("storage: claim cancel %s/%s: %w", executionID, issueID, err)
	}
	if err := tx.Commit(); err != nil {
		return domain.Issue{}, fmt.Errorf("storage: claim cancel %s/%s: %w", executionID, issueID, err)
	}
	return issue, nil
}

// UpdateRetryBudget persists budget's used-counters for issueID within
// executionID. See Store.UpdateRetryBudget's doc comment for why the
// repair loop needs this: TransitionIssue always reloads the Issue fresh
// from execution_issues, so an in-memory-only RecordGateFailure/
// RecordReviewRejection/RecordCIFailure/RecordProviderLimitStop would
// otherwise be silently discarded on the very next transition.
func (s *SQLiteStore) UpdateRetryBudget(ctx context.Context, executionID, issueID string, budget domain.RetryBudget) error {
	return updateRetryBudget(ctx, s.db, executionID, issueID, budget)
}

func updateRetryBudget(ctx context.Context, q querier, executionID, issueID string, budget domain.RetryBudget) error {
	_, err := q.ExecContext(ctx, `
		UPDATE execution_issues
		SET retry_gate_used = ?, retry_review_used = ?, retry_ci_used = ?,
			retry_provider_limit_used = ?
		WHERE execution_id = ? AND issue_id = ?`,
		budget.GateFailures(), budget.ReviewFailures(), budget.CIFailures(),
		budget.ProviderLimitFailures(),
		executionID, issueID,
	)
	if err != nil {
		return fmt.Errorf("storage: update retry budget for issue %s/%s: %w", executionID, issueID, err)
	}
	return nil
}

// ScheduleProviderLimitRetry persists the earliest time an Issue parked in
// PROVIDER_LIMIT may return to READY. A nil retryAt clears the deadline,
// which is what the controller does once it retries the Issue.
//
// This is a narrow method beside UpdateRetryBudget rather than another
// parameter on it. The deadline is a scheduling fact, not a retry counter,
// and the two are written by different callers at different times: the
// engine schedules the deadline, and the controller clears it.
func (s *SQLiteStore) ScheduleProviderLimitRetry(ctx context.Context, executionID, issueID string, retryAt *time.Time) error {
	var value any
	if retryAt != nil {
		value = retryAt.UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE execution_issues
		SET provider_limit_retry_at = ?
		WHERE execution_id = ? AND issue_id = ?`,
		value, executionID, issueID,
	)
	if err != nil {
		return fmt.Errorf("storage: schedule provider-limit retry for issue %s/%s: %w", executionID, issueID, err)
	}
	return nil
}

// ListDueProviderLimitIssues reloads every Issue that is in PROVIDER_LIMIT
// and whose backoff deadline has passed as of now. The query crosses every
// Execution, like ListActiveExecutionLeases, so the reconciliation loop finds
// parked Issues without knowing their Execution IDs in advance.
//
// It returns an empty slice, never nil, when no Issue is due. Dependencies
// are not loaded: the controller only transitions the Issue and redispatches
// its Execution, and the Prepare/Execute path reloads the full Issue itself.
func (s *SQLiteStore) ListDueProviderLimitIssues(ctx context.Context, now time.Time) ([]domain.Issue, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT execution_id, issue_id, provider, title, body, state, scope,
			retry_gate_limit, retry_gate_used,
			retry_review_limit, retry_review_used,
			retry_ci_limit, retry_ci_used,
			retry_provider_limit_limit, retry_provider_limit_used,
			provider_limit_retry_at,
			state_changed_at
		FROM execution_issues
		WHERE state = ? AND provider_limit_retry_at IS NOT NULL AND provider_limit_retry_at <= ?
		ORDER BY execution_id, issue_id`,
		string(domain.StateProviderLimit), now.UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("storage: list due provider-limit issues: %w", err)
	}
	defer func() { _ = rows.Close() }()

	issues := make([]domain.Issue, 0)
	for rows.Next() {
		var executionID string
		issue, err := scanIssueRow(prefixScanner{row: rows, prefix: []any{&executionID}}, "")
		if err != nil {
			return nil, fmt.Errorf("storage: list due provider-limit issues: %w", err)
		}
		issue.ExecutionID = executionID
		issues = append(issues, issue)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: list due provider-limit issues: %w", err)
	}
	return issues, nil
}

// prefixScanner adapts a row that carries extra leading columns to the fixed
// column list scanIssueRow expects. ListDueProviderLimitIssues selects
// execution_id in front of those columns, because the query crosses every
// Execution, and this wrapper consumes that one column before the shared scan
// reads the rest.
type prefixScanner struct {
	row    scanner
	prefix []any
}

func (p prefixScanner) Scan(dest ...any) error {
	all := make([]any, 0, len(p.prefix)+len(dest))
	all = append(all, p.prefix...)
	all = append(all, dest...)
	return p.row.Scan(all...)
}

// updateIssueStateCAS updates an Issue's state only if it still matches
// `from`, returning the number of rows affected (0 or 1). This is a
// compare-and-swap guard on top of the transactional isolation the current
// single-connection SQLite pool already provides — cheap defense-in-depth
// against a future multi-connection or Postgres implementation where a
// naive UPDATE ... WHERE execution_id = ? AND issue_id = ? could silently
// overwrite a state that changed between the read and the write.
func updateIssueStateCAS(ctx context.Context, tx *sql.Tx, executionID, issueID, from, to string, stateChangedAt time.Time) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE execution_issues SET state = ?, state_changed_at = ?
		WHERE execution_id = ? AND issue_id = ? AND state = ?`,
		to, stateChangedAt, executionID, issueID, from,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
