// Package storage persists Forge's domain state transactionally. It is the
// only package in the module allowed to import a SQL driver; domain and
// orchestration code depend on the Store interface defined here, never on
// SQL or a specific database engine. See
// .scratch/forge-mvp/issues/13-sqlite-persistence.md.
package storage

import (
	"context"
	"errors"
	"time"

	"github.com/Teagan42/forge/internal/domain"
)

// ErrNotFound is returned when a lookup finds no matching row.
var ErrNotFound = errors.New("storage: not found")

// ErrAlreadyClaimed is returned by ClaimIssue when the Issue already has a
// Worker claim within the Execution. Enforced at the database level via a
// unique constraint, not by a read-then-write race-prone check.
var ErrAlreadyClaimed = errors.New("storage: issue already claimed")

// ErrConcurrentModification is returned by TransitionIssue when the Issue's
// persisted state no longer matches the state read at the start of the
// transaction — i.e. something else changed it in between. Guards against
// races that a single-connection SQLite pool mostly rules out today but a
// future multi-connection or Postgres implementation would not.
var ErrConcurrentModification = errors.New("storage: issue state changed concurrently")

// Event is a timestamped record of a state transition or other notable
// occurrence, scoped to an Execution and optionally an Issue. Events form
// the append-only audit log described in CONTEXT.md.
type Event struct {
	ID          int64
	ExecutionID string
	IssueID     string // empty for execution-level events
	Type        string
	Data        string
	OccurredAt  time.Time
}

// ExecutionState is an Execution reloaded together with every Issue
// currently recorded against it — the "full state" round-trip the
// acceptance criteria require.
type ExecutionState struct {
	Execution domain.Execution
	Issues    []domain.Issue
}

// Store abstracts Forge's persistence layer. Implementations must persist
// Issue state transitions transactionally, reject duplicate Issue claims,
// and emit an Event for every state transition. No SQL leaks past this
// interface.
type Store interface {
	// Migrate brings the schema up to date. Safe to call on every startup;
	// already-applied migrations are skipped.
	Migrate(ctx context.Context) error

	// CreateExecution persists a new Execution.
	CreateExecution(ctx context.Context, exec domain.Execution) error

	// LoadExecution reloads an Execution and every Issue recorded against
	// it, restoring each Issue's state, scope, dependencies, and retry
	// budget.
	LoadExecution(ctx context.Context, executionID string) (ExecutionState, error)

	// CreateIssue persists a new Issue (with its Dependencies and initial
	// RetryBudget) within an Execution.
	CreateIssue(ctx context.Context, issue domain.Issue) error

	// GetIssue reloads a single Issue by Execution and Issue ID.
	GetIssue(ctx context.Context, executionID, issueID string) (domain.Issue, error)

	// ListIssues reloads every Issue recorded against an Execution.
	ListIssues(ctx context.Context, executionID string) ([]domain.Issue, error)

	// TransitionIssue moves an Issue to a new state and appends a
	// corresponding Event, both inside a single database transaction. The
	// transition is validated against domain.ValidateTransition before any
	// write occurs; an illegal transition leaves persisted state unchanged
	// and returns *domain.InvalidTransitionError.
	TransitionIssue(ctx context.Context, executionID, issueID string, to domain.IssueState) (domain.Issue, error)

	// ClaimIssue records a Worker claim on an Issue and appends a claim
	// Event, transactionally. Returns ErrAlreadyClaimed if the Issue is
	// already claimed within the Execution — enforced by a unique
	// constraint, not a read-then-write check.
	ClaimIssue(ctx context.Context, executionID, issueID, workerRef string) error

	// AppendEvent records a standalone Event (for occurrences that are not
	// Issue state transitions, e.g. execution-level events).
	AppendEvent(ctx context.Context, event Event) error

	// EventsByExecution returns every Event recorded for an Execution,
	// ordered by occurrence time.
	EventsByExecution(ctx context.Context, executionID string) ([]Event, error)

	// EventsByIssue returns every Event recorded for one Issue within an
	// Execution, ordered by occurrence time.
	EventsByIssue(ctx context.Context, executionID, issueID string) ([]Event, error)

	// EventsByTimeRange returns every Event recorded for an Execution whose
	// OccurredAt falls within [from, to], ordered by occurrence time.
	EventsByTimeRange(ctx context.Context, executionID string, from, to time.Time) ([]Event, error)

	// Close releases the underlying database connection(s).
	Close() error
}
