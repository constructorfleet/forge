// Package domain holds Forge's core domain model: the vocabulary defined in
// CONTEXT.md, expressed as Go types. This package has zero infrastructure
// dependencies — no GitHub, Git, Claude, or SQLite imports. It knows nothing
// about how Issues are fetched, how Workspaces are created on disk, or how
// state is persisted; it only models the concepts and their invariants.
package domain

import "time"

// Execution is a user-requested orchestration run over one or more Issues.
// It records a starting base revision for auditing; individual Workers
// capture their own start base when their Issue transitions to READY.
type Execution struct {
	ID           string
	BaseRevision string
	StartedAt    time.Time
}

// IssueRef is Forge's neutral, provider-qualified Issue identity for the few
// cross-provider surfaces that need to disambiguate ID spaces. Issue.ID stays
// the adapter-resolvable, branch/path-safe, human-facing string used by
// existing call sites.
type IssueRef struct {
	Provider string
	ID       string
}

// Dependency is a directed relationship indicating that DependsOnID must
// complete before IssueID can begin. Dependencies form a DAG; cycles are
// errors. A Dependency is satisfied only when the prerequisite Issue's PR is
// merged into the applicable base.
type Dependency struct {
	IssueID      string
	DependsOnID  string
	IssueRef     IssueRef
	DependsOnRef IssueRef
}

// Issue is Forge's normalized representation of an issue-tracker item. All
// internal code operates on Issues, never on tracker-specific models.
type Issue struct {
	ID           string
	Provider     string
	ExecutionID  string
	Title        string
	Body         string
	State        IssueState
	Scope        IssueScope
	Dependencies []Dependency
	RetryBudget  RetryBudget

	// ProviderLimitRetryAt is the earliest time an Issue parked in
	// PROVIDER_LIMIT may return to READY. engine.ProviderLimitController
	// compares it against the clock on each pass. It is nil for every Issue
	// that has no pending provider-limit backoff, which is every Issue in
	// any other state.
	ProviderLimitRetryAt *time.Time
}

// IsManaged reports whether the Issue is part of the Execution set and will
// be executed by a Worker.
func (i Issue) IsManaged() bool { return i.Scope == ScopeManaged }

// IsExternal reports whether the Issue is only observed as a Dependency and
// never executed.
func (i Issue) IsExternal() bool { return i.Scope == ScopeExternal }

// ApplyTransition moves the Issue to `to` if the transition is legal,
// mutating its State. On an illegal transition, the Issue's State is left
// unchanged and the descriptive error is returned.
func (i *Issue) ApplyTransition(to IssueState) error {
	if err := ValidateTransition(i.State, to); err != nil {
		return err
	}
	i.State = to
	return nil
}

// RecordGateFailure records a quality-gate failure against the Issue's
// retry budget. It has a pointer receiver so the increment always lands on
// the addressable Issue, not a value copy — RetryBudget's own mutators are
// pointer-receiver too, so calling them through a non-addressable copy
// (e.g. issue.RetryBudget.RecordGateFailure() on a loop-by-value Issue)
// would silently discard the increment. Callers should mutate through
// *Issue via these wrappers instead of reaching into RetryBudget directly.
func (i *Issue) RecordGateFailure() error { return i.RetryBudget.RecordGateFailure() }

// RecordReviewRejection records a review rejection against the Issue's
// retry budget. See RecordGateFailure for why this must be a pointer
// receiver.
func (i *Issue) RecordReviewRejection() error { return i.RetryBudget.RecordReviewRejection() }

// RecordCIFailure records a CI failure against the Issue's retry budget.
// See RecordGateFailure for why this must be a pointer receiver.
func (i *Issue) RecordCIFailure() error { return i.RetryBudget.RecordCIFailure() }

// RecordProviderLimitStop records a provider rate or quota limit against the
// Issue's retry budget. See RecordGateFailure for why this must be a pointer
// receiver.
func (i *Issue) RecordProviderLimitStop() error { return i.RetryBudget.RecordProviderLimitStop() }

// Workspace is the isolated environment associated with a single Issue
// execution. Currently implemented as a Git worktree, but the domain
// concept is the isolation boundary, not the Git mechanism.
type Workspace struct {
	IssueID string
	Path    string
	Branch  string
}

// ConflictCandidate is a disposable Workspace and branch used only for one
// automatic pull-request conflict repair attempt.
type ConflictCandidate struct {
	Path    string
	Branch  string
	HeadSHA string
}

// Worker is one coding-agent invocation responsible for one Issue within an
// Execution. The orchestrator's unit of concurrent work.
type Worker struct {
	IssueID     string
	ExecutionID string
	Workspace   Workspace
}
