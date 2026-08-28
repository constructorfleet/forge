package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	_ "modernc.org/sqlite" // pure-Go SQLite driver, registers as "sqlite"
)

// SQLiteStore is the SQLite-backed implementation of Store. It is the only
// place in the module that issues SQL.
type SQLiteStore struct {
	db *sql.DB
}

// Open opens (creating if necessary) a SQLite database at dsn and returns a
// Store backed by it. dsn is passed directly to modernc.org/sqlite, so both
// file paths and "file::memory:?cache=shared"-style DSNs work.
func Open(dsn string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("storage: open %s: %w", dsn, err)
	}
	// SQLite allows only one writer at a time; serialize through a single
	// connection so concurrent callers wait rather than hit SQLITE_BUSY.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("storage: enable foreign_keys: %w", err)
	}

	return &SQLiteStore{db: db}, nil
}

// Close releases the underlying database connection.
func (s *SQLiteStore) Close() error { return s.db.Close() }

// Migrate brings the schema up to date. Safe to call repeatedly.
func (s *SQLiteStore) Migrate(ctx context.Context) error {
	return migrate(ctx, s.db)
}

// CreateExecution persists a new Execution.
func (s *SQLiteStore) CreateExecution(ctx context.Context, exec domain.Execution) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO executions (id, base_revision, started_at)
		VALUES (?, ?, ?)`,
		exec.ID, exec.BaseRevision, exec.StartedAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("storage: create execution %s: %w", exec.ID, err)
	}
	return nil
}

// LoadExecution reloads an Execution and every Issue recorded against it.
func (s *SQLiteStore) LoadExecution(ctx context.Context, executionID string) (ExecutionState, error) {
	exec, err := s.getExecution(ctx, s.db, executionID)
	if err != nil {
		return ExecutionState{}, err
	}
	issues, err := s.ListIssues(ctx, executionID)
	if err != nil {
		return ExecutionState{}, err
	}
	return ExecutionState{Execution: exec, Issues: issues}, nil
}

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (s *SQLiteStore) getExecution(ctx context.Context, q querier, executionID string) (domain.Execution, error) {
	row := q.QueryRowContext(ctx, `
		SELECT id, base_revision, started_at FROM executions WHERE id = ?`,
		executionID,
	)
	var (
		exec      domain.Execution
		startedAt time.Time
	)
	if err := row.Scan(&exec.ID, &exec.BaseRevision, &startedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Execution{}, fmt.Errorf("storage: execution %s: %w", executionID, ErrNotFound)
		}
		return domain.Execution{}, fmt.Errorf("storage: load execution %s: %w", executionID, err)
	}
	exec.StartedAt = startedAt.UTC()
	return exec, nil
}

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

	return tx.Commit()
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

	issue, err := scanIssue(row, executionID)
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

func scanIssue(row *sql.Row, executionID string) (domain.Issue, error) {
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

// ListIssues reloads every Issue recorded against an Execution.
func (s *SQLiteStore) ListIssues(ctx context.Context, executionID string) ([]domain.Issue, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT issue_id FROM execution_issues
		WHERE execution_id = ?
		ORDER BY issue_id`,
		executionID,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: list issues for execution %s: %w", executionID, err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("storage: list issues for execution %s: %w", executionID, err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("storage: list issues for execution %s: %w", executionID, err)
	}
	_ = rows.Close()

	issues := make([]domain.Issue, 0, len(ids))
	for _, id := range ids {
		issue, err := s.getIssue(ctx, s.db, executionID, id)
		if err != nil {
			return nil, err
		}
		issues = append(issues, issue)
	}
	return issues, nil
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

	if _, err := tx.ExecContext(ctx, `
		UPDATE execution_issues SET state = ? WHERE execution_id = ? AND issue_id = ?`,
		string(issue.State), executionID, issueID,
	); err != nil {
		return domain.Issue{}, fmt.Errorf("storage: transition issue %s/%s: %w", executionID, issueID, err)
	}

	event := Event{
		ExecutionID: executionID,
		IssueID:     issueID,
		Type:        "issue.transitioned",
		Data:        fmt.Sprintf(`{"from":%q,"to":%q}`, from, to),
		OccurredAt:  time.Now().UTC(),
	}
	if err := insertEvent(ctx, tx, event); err != nil {
		return domain.Issue{}, fmt.Errorf("storage: transition issue %s/%s: %w", executionID, issueID, err)
	}

	if err := tx.Commit(); err != nil {
		return domain.Issue{}, fmt.Errorf("storage: transition issue %s/%s: %w", executionID, issueID, err)
	}
	return issue, nil
}

// ClaimIssue records a Worker claim on an Issue and appends a claim Event,
// transactionally. The workers.UNIQUE(execution_id, issue_id) constraint is
// the actual duplicate-claim guard; this method translates that constraint
// violation into ErrAlreadyClaimed.
func (s *SQLiteStore) ClaimIssue(ctx context.Context, executionID, issueID, workerRef string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: claim issue %s/%s: %w", executionID, issueID, err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workers (execution_id, issue_id, worker_ref, claimed_at)
		VALUES (?, ?, ?, ?)`,
		executionID, issueID, workerRef, now,
	); err != nil {
		if isUniqueConstraintErr(err) {
			return fmt.Errorf("storage: claim issue %s/%s: %w", executionID, issueID, ErrAlreadyClaimed)
		}
		return fmt.Errorf("storage: claim issue %s/%s: %w", executionID, issueID, err)
	}

	event := Event{
		ExecutionID: executionID,
		IssueID:     issueID,
		Type:        "issue.claimed",
		Data:        fmt.Sprintf(`{"worker_ref":%q}`, workerRef),
		OccurredAt:  now,
	}
	if err := insertEvent(ctx, tx, event); err != nil {
		return fmt.Errorf("storage: claim issue %s/%s: %w", executionID, issueID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: claim issue %s/%s: %w", executionID, issueID, err)
	}
	return nil
}

func isUniqueConstraintErr(err error) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// TableExists reports whether table is present in the SQLite schema.
// Exported for migration tests, which assert every table the ticket
// requires was actually created.
func TableExists(ctx context.Context, s *SQLiteStore, table string) error {
	var name string
	row := s.db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table)
	if err := row.Scan(&name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("does not exist")
		}
		return err
	}
	return nil
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
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, execution_id, COALESCE(issue_id, ''), type, data, occurred_at
		FROM events WHERE execution_id = ?
		ORDER BY occurred_at, id`,
		executionID,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: events for execution %s: %w", executionID, err)
	}
	return scanEvents(rows)
}

// EventsByIssue returns every Event recorded for one Issue within an
// Execution, ordered by occurrence time.
func (s *SQLiteStore) EventsByIssue(ctx context.Context, executionID, issueID string) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, execution_id, COALESCE(issue_id, ''), type, data, occurred_at
		FROM events WHERE execution_id = ? AND issue_id = ?
		ORDER BY occurred_at, id`,
		executionID, issueID,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: events for issue %s/%s: %w", executionID, issueID, err)
	}
	return scanEvents(rows)
}

// EventsByTimeRange returns every Event recorded for an Execution whose
// OccurredAt falls within [from, to], ordered by occurrence time.
func (s *SQLiteStore) EventsByTimeRange(ctx context.Context, executionID string, from, to time.Time) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, execution_id, COALESCE(issue_id, ''), type, data, occurred_at
		FROM events WHERE execution_id = ? AND occurred_at BETWEEN ? AND ?
		ORDER BY occurred_at, id`,
		executionID, from.UTC(), to.UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("storage: events for execution %s in range: %w", executionID, err)
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
