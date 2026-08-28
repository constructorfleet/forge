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

// GateRun is one executed Quality Gate's persisted result, scoped to an
// Execution and Issue (CONTEXT.md "Gate Runner"). It mirrors
// internal/gate.Result but lives in this package so storage has no
// dependency on internal/gate — callers (the engine) translate between the
// two.
type GateRun struct {
	ExecutionID string
	IssueID     string
	Name        string
	Command     string
	StartedAt   time.Time
	FinishedAt  time.Time
	ExitCode    int
	Stdout      string
	Stderr      string
	Passed      bool
}

// ReviewFinding is one structured Finding raised during a ReviewRun,
// mirroring review.Finding but living in this package so storage has no
// dependency on internal/review — callers (the engine) translate between
// the two, the same convention GateRun documents for internal/gate.
type ReviewFinding struct {
	Severity string
	File     string
	Line     int
	Message  string
}

// ReviewRun is one Review invocation's persisted outcome (CONTEXT.md
// "Review"), scoped to an Execution and Issue: the diff it evaluated, its
// verdict, and any structured Findings when CHANGES_REQUIRED.
type ReviewRun struct {
	ExecutionID string
	IssueID     string
	Verdict     string
	Summary     string
	Diff        string
	StartedAt   time.Time
	FinishedAt  time.Time
	Findings    []ReviewFinding
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

	// UpdateRetryBudget persists budget's current used-counters (gate,
	// review, CI) for issueID within executionID; the configured limits are
	// immutable after CreateIssue and are not touched. Ticket 21's repair
	// loop calls this immediately after incrementing a counter in memory
	// (domain.Issue.RecordGateFailure/RecordReviewRejection/RecordCIFailure)
	// so a subsequent TransitionIssue/GetIssue reload reflects the new
	// count rather than silently reverting it to what CreateIssue wrote.
	UpdateRetryBudget(ctx context.Context, executionID, issueID string, budget domain.RetryBudget) error

	// ClaimIssue records a Worker claim on an Issue and appends a claim
	// Event, transactionally. Returns ErrAlreadyClaimed if the Issue is
	// already claimed within the Execution — enforced by a unique
	// constraint, not a read-then-write check.
	ClaimIssue(ctx context.Context, executionID, issueID, workerRef string) error

	// AppendEvent records a standalone Event (for occurrences that are not
	// Issue state transitions, e.g. execution-level events).
	AppendEvent(ctx context.Context, event Event) error

	// RecordGateRun persists one executed Quality Gate Result and appends a
	// "gate.run" Event, transactionally. See CONTEXT.md "Gate Runner".
	RecordGateRun(ctx context.Context, run GateRun) error

	// GateRunsByIssue returns every GateRun recorded for one Issue within an
	// Execution, ordered by insertion (i.e. execution order).
	GateRunsByIssue(ctx context.Context, executionID, issueID string) ([]GateRun, error)

	// RecordReviewRun persists one Review invocation's outcome (CONTEXT.md
	// "Review"), including its Findings, and appends a "review.run" Event,
	// transactionally.
	RecordReviewRun(ctx context.Context, run ReviewRun) error

	// ReviewRunsByIssue returns every ReviewRun recorded for one Issue
	// within an Execution, ordered by insertion (i.e. execution order), each
	// with its Findings populated.
	ReviewRunsByIssue(ctx context.Context, executionID, issueID string) ([]ReviewRun, error)

	// EventsByExecution returns every Event recorded for an Execution,
	// ordered by occurrence time.
	EventsByExecution(ctx context.Context, executionID string) ([]Event, error)

	// EventsByIssue returns every Event recorded for one Issue within an
	// Execution, ordered by occurrence time.
	EventsByIssue(ctx context.Context, executionID, issueID string) ([]Event, error)

	// EventsByTimeRange returns every Event recorded for an Execution whose
	// OccurredAt falls within [from, to], ordered by occurrence time.
	EventsByTimeRange(ctx context.Context, executionID string, from, to time.Time) ([]Event, error)

	// SaveNeedsInfoCheckpoint persists (inserting or replacing) the
	// needs-info checkpoint for one Issue within an Execution: the question
	// asked, why, when, and which of the label/comment side effects have
	// already run — so the NEEDS_INFO handling in internal/engine stays
	// idempotent across repeats and `forge resume` can detect new human
	// input since the checkpoint (see CONTEXT.md's needs-info resume flow,
	// issue 07).
	SaveNeedsInfoCheckpoint(ctx context.Context, checkpoint NeedsInfoCheckpoint) error

	// GetNeedsInfoCheckpoint reloads the needs-info checkpoint for one Issue
	// within an Execution. Returns ErrNotFound if none has been recorded.
	GetNeedsInfoCheckpoint(ctx context.Context, executionID, issueID string) (NeedsInfoCheckpoint, error)

	// Close releases the underlying database connection(s).
	Close() error
}

// NeedsInfoCheckpoint is the persisted record of an Issue's transition to
// NEEDS_INFO: the question asked, the context it arose from, when, and the
// state of the idempotent label/comment side effects.
//
// CommentAuthor/CommentPostedAt are the tracker-reported (server-clock)
// identity and timestamp of forge's own posted comment, returned by
// tracker.Tracker.AddComment — NOT locally captured — so `forge resume` can
// (a) compare candidate "new" comments against the same clock the tracker
// itself stamped them with, avoiding false positives from local/tracker
// clock skew, and (b) exclude forge's own comment from the "new human
// input" check by author. Both are zero/empty if no comment was ever
// posted (e.g. Blocked.Comment is configured false).
//
// ResumedAt/ResumedContext are populated once `forge resume` detects new
// human input and moves the Issue back to READY — ResumedContext is the
// JSON-encoded focused context (original Issue + previous question + only
// the new comments). It is currently write-only: no ticket yet re-drives a
// resumed Issue through Execute, so this is a forward seam for a future
// re-execution path (tickets 21/24 territory) rather than something read
// back today.
type NeedsInfoCheckpoint struct {
	ExecutionID     string
	IssueID         string
	Question        string
	Context         string
	LabelAdded      bool
	CommentPosted   bool
	CommentAuthor   string
	CommentPostedAt time.Time
	CreatedAt       time.Time
	ResumedAt       *time.Time
	ResumedContext  string
}
