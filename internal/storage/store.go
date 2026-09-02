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

// ClaimConflictError reports that an Issue already has an active Worker
// claim owned by another Execution.
type ClaimConflictError struct {
	IssueID           string
	OwningExecutionID string
	OwningWorkerRef   string
}

func (e *ClaimConflictError) Error() string {
	if e == nil {
		return ErrAlreadyClaimed.Error()
	}
	if e.OwningExecutionID == "" {
		return ErrAlreadyClaimed.Error()
	}
	return "storage: issue " + e.IssueID + " already claimed by execution " + e.OwningExecutionID
}

func (e *ClaimConflictError) Unwrap() error { return ErrAlreadyClaimed }

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

// AgentRun is one persisted implementation-agent invocation for an Issue.
// Review remains a separate first-class record via ReviewRun.
type AgentRun struct {
	ExecutionID  string
	IssueID      string
	Backend      string
	StartedAt    time.Time
	FinishedAt   time.Time
	Result       string
	ContextBytes int
	InputTokens  *int
	OutputTokens *int
}

// TranscriptEvent is one persisted step of an Agent's work on an Issue
// during one AgentRun (ticket 28), mirroring internal/agent.TranscriptEvent
// so storage has no dependency on internal/agent — callers (the engine)
// translate between the two, the same convention GateRun/ReviewRun
// document.
type TranscriptEvent struct {
	ExecutionID string
	IssueID     string
	AgentRunID  int64
	Seq         int
	Type        string
	Role        string
	Text        string
	ToolName    string
	ToolInput   string
	ToolOutput  string
	// ToolCallID pairs a TOOL_RESULT back to the TOOL_CALL that produced it
	// (issue 36). Set to the tool-use id on both a TOOL_CALL (its own id)
	// and its matching TOOL_RESULT (the id it references); empty otherwise.
	ToolCallID string
	OccurredAt time.Time
	// Phase names the workflow phase this event was captured during (e.g.
	// "IMPLEMENTING", "REVIEWING"), and Subagent names which subagent
	// produced it within that phase (a review axis such as "bugs",
	// "quality", "docs"; empty for the single implementation agent) — issue
	// #219, so a row is self-describing once both the execution agent and
	// the review agent's axes share this table.
	Phase    string
	Subagent string
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

// ReviewAxisEnvelope is one review axis's ("bugs", "quality", "docs")
// full audit record for a single ReviewRun (issue #162): whether it ran to
// completion (mirroring review.AxisCoverage) and why not when it didn't,
// its token usage when the backend exposed one, and its raw envelope
// exactly as that axis's agent emitted it, before synthesis deduped/folded
// its findings into ReviewRun.Findings (and, since issue #176/#182, before
// its assurances were folded into assurance-vs-finding tension detection).
// RawEnvelope is kept as an opaque JSON-encoded blob (a JSON object shaped
// {"findings": [...], "assurances": [...]}) rather than individually
// queryable columns like ReviewFinding — mirroring how GateRun keeps
// stdout/stderr as plain text — since this is audit/reconstruction detail,
// not data any query filters on directly; the caller (the engine) is
// responsible for producing and parsing that JSON, so storage has no
// dependency on internal/review, the same convention ReviewFinding
// documents.
type ReviewAxisEnvelope struct {
	Axis   string
	Ran    bool
	Reason string

	// InputTokens/OutputTokens are nil when the axis didn't run or its
	// backend exposed no token accounting for that invocation.
	InputTokens  *int
	OutputTokens *int

	// RawEnvelope is the axis's raw findings+assurances envelope,
	// JSON-encoded. Empty when the axis did not run.
	RawEnvelope string
}

// ReviewRun is one Review invocation's persisted outcome (CONTEXT.md
// "Review"), scoped to an Execution and Issue: the diff it evaluated, its
// verdict, any structured Findings when CHANGES_REQUIRED, and — since issue
// #162 — every axis's full raw envelope for after-the-fact reconstruction.
type ReviewRun struct {
	ExecutionID string
	IssueID     string
	Verdict     string
	Summary     string
	Diff        string
	StartedAt   time.Time
	FinishedAt  time.Time
	Findings    []ReviewFinding
	Envelopes   []ReviewAxisEnvelope
}

// PullRequest is one created (or idempotently recovered) pull request's
// persisted record, mirroring tracker.PullRequest but living in this
// package so storage has no dependency on internal/tracker — callers (the
// engine) translate between the two, the same convention GateRun/ReviewRun
// document for internal/gate and internal/review.
type PullRequest struct {
	ExecutionID string
	IssueID     string
	Number      int
	URL         string
	// BaseBranch is the pull request target branch when known. It can be
	// empty on pre-0024 or partially recovered records.
	BaseBranch string
	// CommitSHA is the HEAD commit the Publisher committed and pushed
	// immediately before this pull request was created (CONTEXT.md
	// "COMMITTING", "PR_CREATING").
	CommitSHA string
	CreatedAt time.Time
}

// ConflictResolutionAttempt records one automatic merge-conflict repair
// candidate after Forge has published it, so downstream CI/review failure
// can restore the pull request branch to the original recorded head instead
// of entering the ordinary CI repair loop.
type ConflictResolutionAttempt struct {
	ExecutionID  string
	IssueID      string
	PRNumber     int
	Branch       string
	OriginalSHA  string
	CandidateSHA string
	Status       string
	Details      string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// WorkerClaim is one active Worker claim on an Issue, including the owning
// process ID used by restart recovery to distinguish live workers from
// orphaned ones after a crash or termination.
type WorkerClaim struct {
	ExecutionID string
	IssueID     string
	WorkerRef   string
	OwnerPID    int
	ClaimedAt   time.Time
}

// CIRunStatus is the normalized result of one CI poll attempt.
type CIRunStatus string

const (
	CIRunStatusPending CIRunStatus = "PENDING"
	CIRunStatusPassed  CIRunStatus = "PASSED"
	CIRunStatusFailed  CIRunStatus = "FAILED"
)

// CIRunKind discriminates what triggered a CIRun. The zero value ("")
// means a required-check evaluation (CIRunKindCheck) — every CIRun
// recorded before issue 109's PR-supervision work, and the default for
// callers that never set it, so existing persisted rows and call sites
// keep behaving exactly as before.
type CIRunKind string

const (
	// CIRunKindCheck is the zero value: a required-check (CI) evaluation,
	// the pre-existing behavior for every CIRun recorded before issue
	// 109's PR-supervision work and for every call site that never sets
	// Kind explicitly.
	CIRunKindCheck CIRunKind = ""
	// CIRunKindReview is an actionable pull-request review requesting
	// changes.
	CIRunKindReview CIRunKind = "review"
	// CIRunKindConflict is a detected merge conflict between the pull
	// request's branch and its base.
	CIRunKindConflict CIRunKind = "conflict"
	// CIRunKindStale is a detected staleness (the pull request's branch
	// has fallen behind its base branch) that Wait attempted to remediate
	// with an automatic rebase (issue 233).
	CIRunKindStale CIRunKind = "stale"
)

// CIRun is one persisted PR supervision attempt for an Issue in CI_PENDING
// (CONTEXT.md "CI Supervisor"; issue 109 extends this beyond required
// checks to actionable PR reviews and merge conflicts — see Kind).
type CIRun struct {
	ExecutionID string
	IssueID     string
	Status      CIRunStatus
	// Kind discriminates what this run evaluated. Empty is treated as
	// CIRunKindCheck (see CIRunKind's doc comment).
	Kind      CIRunKind
	CheckName string
	Details   string
	CheckedAt time.Time
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

	// ListExecutions reloads every persisted Execution together with its
	// recorded Issues.
	ListExecutions(ctx context.Context) ([]ExecutionState, error)

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
	// Event, transactionally. Returns ErrAlreadyClaimed when the Issue is
	// already actively claimed; when the claimant belongs to another
	// Execution, the concrete error also unwraps as *ClaimConflictError so
	// callers can report the owning Execution explicitly.
	ClaimIssue(ctx context.Context, executionID, issueID, workerRef string) error

	// UpdateWorkerOwner records the OS process ID currently owning the
	// active Worker claim for issueID within executionID.
	UpdateWorkerOwner(ctx context.Context, executionID, issueID string, ownerPID int) error

	// WorkerClaim reloads the active Worker claim for issueID within
	// executionID. Returns ErrNotFound if no active claim exists.
	WorkerClaim(ctx context.Context, executionID, issueID string) (WorkerClaim, error)

	// ReleaseWorkerClaim removes the active Worker claim for issueID within
	// executionID. Releasing a missing claim is a no-op.
	ReleaseWorkerClaim(ctx context.Context, executionID, issueID string) error

	// AppendEvent records a standalone Event (for occurrences that are not
	// Issue state transitions, e.g. execution-level events).
	AppendEvent(ctx context.Context, event Event) error

	// RecordWorkspace persists the current Workspace path/branch for issueID
	// within executionID, replacing any earlier record.
	RecordWorkspace(ctx context.Context, executionID string, ws domain.Workspace) error

	// WorkspaceByIssue reloads the persisted Workspace for issueID within
	// executionID. Returns ErrNotFound if none has been recorded.
	WorkspaceByIssue(ctx context.Context, executionID, issueID string) (domain.Workspace, error)

	// RecordGateRun persists one executed Quality Gate Result and appends a
	// "gate.run" Event, transactionally. See CONTEXT.md "Gate Runner".
	RecordGateRun(ctx context.Context, run GateRun) error

	// RecordAgentRun persists one implementation Agent invocation and
	// appends a corresponding "agent.run" Event, returning the
	// storage-assigned AgentRun id so callers can key TranscriptEvents to
	// this specific attempt via RecordTranscriptEvents.
	RecordAgentRun(ctx context.Context, run AgentRun) (int64, error)

	// StartAgentRun inserts an in-progress AgentRun row up front — before
	// the Agent is invoked — and returns its id, so transcript events can be
	// persisted incrementally against it as they stream (issue 36) rather
	// than only in a single batch at the end. run.Result/FinishedAt/tokens
	// are not yet known: the row records a RUNNING result and a placeholder
	// finished_at until FinalizeAgentRun updates it. No "agent.run" Event is
	// appended here; that stays FinalizeAgentRun's job so the audit log's
	// event still marks completion, exactly as RecordAgentRun did.
	StartAgentRun(ctx context.Context, run AgentRun) (int64, error)

	// FinalizeAgentRun updates the AgentRun row StartAgentRun created with
	// its terminal result, finished_at, and token usage, and appends the
	// "agent.run" Event — the completion half of the run lifecycle whose
	// start was StartAgentRun. A run that is never finalized (e.g. the
	// process died mid-invocation) keeps its RUNNING result, a durable
	// signal that it was interrupted.
	FinalizeAgentRun(ctx context.Context, agentRunID int64, run AgentRun) error

	// AgentRunsByExecution returns every AgentRun recorded for one
	// Execution, ordered by insertion.
	AgentRunsByExecution(ctx context.Context, executionID string) ([]AgentRun, error)

	// AgentRunsByIssue returns every AgentRun recorded for one Issue within
	// an Execution, ordered by insertion.
	AgentRunsByIssue(ctx context.Context, executionID, issueID string) ([]AgentRun, error)

	// RecordTranscriptEvents persists every TranscriptEvent captured during
	// one AgentRun (ticket 28's transcript logging), keyed by the AgentRun
	// id RecordAgentRun returned. Capture is best-effort by contract at the
	// call site (internal/engine): a failure here must never fail the
	// Issue's run, only forfeit that attempt's transcript durability.
	RecordTranscriptEvents(ctx context.Context, executionID, issueID string, agentRunID int64, events []TranscriptEvent) error

	// ReplaceTranscriptEvents overwrites the persisted transcript for one
	// AgentRun with events, in a single transaction: it deletes any rows
	// already stored for the run, then inserts events in order. Incremental
	// capture (issue 36) flushes a bounded, most-recent window repeatedly as
	// an Agent streams, so each flush must supersede the last rather than
	// append — that is how the persisted transcript stays a faithful window
	// (with its dropped-earlier marker) instead of accumulating stale rows.
	ReplaceTranscriptEvents(ctx context.Context, executionID, issueID string, agentRunID int64, events []TranscriptEvent) error

	// TranscriptEventsByAgentRun returns every TranscriptEvent recorded for
	// one AgentRun (attempt), ordered by Seq.
	TranscriptEventsByAgentRun(ctx context.Context, executionID, issueID string, agentRunID int64) ([]TranscriptEvent, error)

	// TranscriptEventsByIssue returns every TranscriptEvent recorded for an
	// Issue across every AgentRun (attempt), ordered chronologically — the
	// read surface ticket 28 requires (e.g. `forge status <exec> <issue>
	// --transcript`).
	TranscriptEventsByIssue(ctx context.Context, executionID, issueID string) ([]TranscriptEvent, error)

	// GateRunsByIssue returns every GateRun recorded for one Issue within an
	// Execution, ordered by insertion (i.e. execution order).
	GateRunsByIssue(ctx context.Context, executionID, issueID string) ([]GateRun, error)

	// RecordPullRequest persists one created pull request and appends a
	// "pull_request.created" Event, transactionally. An identical recovered
	// record is a no-op. A recovered record can backfill a missing
	// BaseBranch without a new creation Event.
	RecordPullRequest(ctx context.Context, pr PullRequest) error

	// PullRequestsByIssue returns every PullRequest recorded for one Issue
	// within an Execution, ordered by insertion.
	PullRequestsByIssue(ctx context.Context, executionID, issueID string) ([]PullRequest, error)

	// RecordConflictResolutionAttempt persists one automatic
	// merge-conflict repair candidate that has been published to the pull
	// request branch.
	RecordConflictResolutionAttempt(ctx context.Context, attempt ConflictResolutionAttempt) error

	// ActiveConflictResolutionAttempt returns the latest published
	// automatic merge-conflict repair candidate for one Issue. It returns
	// ErrNotFound when no published candidate is awaiting downstream CI or
	// review outcome.
	ActiveConflictResolutionAttempt(ctx context.Context, executionID, issueID string) (ConflictResolutionAttempt, error)

	// UpdateConflictResolutionAttemptStatus updates the active conflict
	// repair attempt's terminal status and details.
	UpdateConflictResolutionAttemptStatus(ctx context.Context, executionID, issueID, status, details string, updatedAt time.Time) error

	// RecordCIRun persists one CI supervision attempt and appends a
	// corresponding "ci.run" Event.
	RecordCIRun(ctx context.Context, run CIRun) error

	// CIRunsByIssue returns every CIRun recorded for one Issue within an
	// Execution, ordered by insertion.
	CIRunsByIssue(ctx context.Context, executionID, issueID string) ([]CIRun, error)

	// RecordReviewRun persists one Review invocation's outcome (CONTEXT.md
	// "Review"), including its Findings, and appends a "review.run" Event,
	// transactionally.
	RecordReviewRun(ctx context.Context, run ReviewRun) error

	// ReviewRunsByIssue returns every ReviewRun recorded for one Issue
	// within an Execution, ordered by insertion (i.e. execution order), each
	// with its Findings populated.
	ReviewRunsByIssue(ctx context.Context, executionID, issueID string) ([]ReviewRun, error)

	// RecordReviewOverride persists a non-convergent review finding (issue
	// #375), keyed by IssueID alone so it survives into a new Execution for
	// the same Issue/branch. Recording the same (IssueID, Signature) again
	// is a no-op.
	RecordReviewOverride(ctx context.Context, override domain.ReviewOverride) error

	// ReviewOverridesByIssue returns every ReviewOverride recorded for one
	// Issue, across all Executions, ordered by insertion.
	ReviewOverridesByIssue(ctx context.Context, issueID string) ([]domain.ReviewOverride, error)

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

	// SaveStatusSignalCheckpoint persists (inserting or replacing) whether
	// the ticket-24 status-reflection start comment has been posted for one
	// Issue within an Execution (internal/statusreflect).
	SaveStatusSignalCheckpoint(ctx context.Context, checkpoint StatusSignalCheckpoint) error

	// GetStatusSignalCheckpoint reloads the status-signal checkpoint for one
	// Issue within an Execution. Returns ErrNotFound if none has been
	// recorded.
	GetStatusSignalCheckpoint(ctx context.Context, executionID, issueID string) (StatusSignalCheckpoint, error)

	// CreatePlanningExecution persists a new Planning Execution (ticket 11's
	// runtime container for `forge plan`, scoped to a Feature rather than
	// coding Issues).
	CreatePlanningExecution(ctx context.Context, exec domain.PlanningExecution) error

	// LoadPlanningExecution reloads a single Planning Execution by ID.
	LoadPlanningExecution(ctx context.Context, executionID string) (domain.PlanningExecution, error)

	// ListPlanningExecutionsByFeature reloads every Planning Execution
	// recorded for featureID, ordered by start time.
	ListPlanningExecutionsByFeature(ctx context.Context, featureID string) ([]domain.PlanningExecution, error)

	// UpdatePlanningStatus persists a Planning Execution's current runtime
	// Status (ACTIVE/NEEDS_HUMAN/NEEDS_APPROVAL/FAILED/COMPLETE). Stage and
	// artifact freshness are deliberately not part of this call: they are
	// derived from the Feature's Planning Artifacts on disk
	// (internal/planning) every time they're needed, never persisted.
	UpdatePlanningStatus(ctx context.Context, executionID string, status domain.PlanningStatus) error

	// ClaimFeaturePlanningLease records a Planning Execution's lease on
	// featureID, enforcing at most one active Planning Execution per
	// Feature. Returns ErrPlanningLeaseHeld (unwrappable to
	// *PlanningLeaseConflictError) if featureID already has an active
	// lease, via a database constraint rather than a read-then-write check
	// that would race. Implementation Issue claims (workers) are a separate
	// table and are unaffected by Feature planning leases.
	ClaimFeaturePlanningLease(ctx context.Context, featureID, executionID string) error

	// FeaturePlanningLease reloads the active planning lease for featureID.
	// Returns ErrNotFound if no active lease exists.
	FeaturePlanningLease(ctx context.Context, featureID string) (PlanningLease, error)

	// UpdatePlanningLeaseOwner records the OS process ID currently owning
	// the active planning lease for featureID, the lease analogue of
	// UpdateWorkerOwner.
	UpdatePlanningLeaseOwner(ctx context.Context, featureID string, ownerPID int) error

	// ReleaseFeaturePlanningLease removes the active planning lease for
	// featureID. Releasing a missing lease is a no-op.
	ReleaseFeaturePlanningLease(ctx context.Context, featureID string) error

	// FreezeFeature records (or refreshes) a Feature's replan freeze
	// (ticket 22): while it exists, no new Issue belonging to the Feature is
	// admitted for execution and no in-flight Worker may integrate its work.
	// Idempotent by feature_id, and deliberately independent of
	// feature_planning_leases so the freeze can be persisted *before* the
	// planning lease is acquired.
	FreezeFeature(ctx context.Context, featureID, reason, triggeringIssueID string) error

	// IsFeatureFrozen reports whether a Feature currently has a replan
	// freeze, returning the freeze itself when it does. An empty featureID
	// (an Issue with no Forge Provenance block) is never frozen.
	IsFeatureFrozen(ctx context.Context, featureID string) (bool, FeatureFreeze, error)

	// UnfreezeFeature removes a Feature's replan freeze, letting frozen work
	// resume. Callers must only do this once a fresh plan has been approved
	// and superseded Issues have been closed (see cmd/forge's approve
	// tickets path). Unfreezing an unfrozen Feature is a no-op.
	UnfreezeFeature(ctx context.Context, featureID string) error

	// SaveReplanCheckpoint persists (inserting or replacing) the
	// REPLAN_REQUIRED checkpoint for one Issue within an Execution: the
	// structured trigger the Agent reported and which of the freeze /
	// planning-lease / Decision side effects have already run, so the
	// handling in internal/engine stays idempotent across repeats.
	SaveReplanCheckpoint(ctx context.Context, checkpoint ReplanCheckpoint) error

	// GetReplanCheckpoint reloads the replan checkpoint for one Issue within
	// an Execution. Returns ErrNotFound if none has been recorded.
	GetReplanCheckpoint(ctx context.Context, executionID, issueID string) (ReplanCheckpoint, error)

	// SaveDecisionCheckpoint persists (inserting or replacing) the
	// NEEDS_HUMAN checkpoint for one Decision within a Planning Execution:
	// the question asked, the Decision provenance it arose from, when, and
	// which of the label/comment side effects have already run -- so the
	// NEEDS_HUMAN handling in internal/wayfinding stays idempotent across
	// repeats (ticket 15a).
	SaveDecisionCheckpoint(ctx context.Context, checkpoint DecisionCheckpoint) error

	// GetDecisionCheckpoint reloads the NEEDS_HUMAN checkpoint for one
	// Decision within a Planning Execution. Returns ErrNotFound if none
	// has been recorded.
	GetDecisionCheckpoint(ctx context.Context, executionID, decisionID string) (DecisionCheckpoint, error)

	// GetDecisionCheckpointsByExecution reloads all NEEDS_HUMAN checkpoints
	// for a Planning Execution.
	GetDecisionCheckpointsByExecution(ctx context.Context, executionID string) ([]DecisionCheckpoint, error)

	// ClaimExecutionLease records a remote execution's lease on issueID
	// within executionID, following ClaimFeaturePlanningLease's pattern
	// plus an initial heartbeat and expiresAt. Returns ErrExecutionLeaseHeld
	// (unwrappable to *ExecutionLeaseConflictError) if a lease is already
	// held, via a database constraint rather than a read-then-write check
	// that would race.
	ClaimExecutionLease(ctx context.Context, executionID, issueID string, expiresAt time.Time) error

	// ExecutionLease reloads the active execution lease for issueID within
	// executionID. Returns ErrNotFound if no active lease exists.
	ExecutionLease(ctx context.Context, executionID, issueID string) (ExecutionLease, error)

	// HeartbeatExecutionLease records that the worker is still alive,
	// advancing the lease's heartbeat to now and its expiry to expiresAt.
	// Returns ErrNotFound if no active lease exists.
	HeartbeatExecutionLease(ctx context.Context, executionID, issueID string, expiresAt time.Time) error

	// ReleaseExecutionLease removes the active execution lease for issueID
	// within executionID. Releasing a missing lease is a no-op.
	ReleaseExecutionLease(ctx context.Context, executionID, issueID string) error

	// ListActiveExecutionLeases reloads every active execution lease across
	// all Executions and Issues, letting a periodic loss-detection loop
	// (issue #400) find in-flight remote executions to check without
	// knowing their IDs in advance. Returns an empty slice, never nil, when
	// no lease is held.
	ListActiveExecutionLeases(ctx context.Context) ([]ExecutionLease, error)

	// RecordExecutionPlacement persists the remote-execution substrate
	// facts for one Issue execution — backend, worker, Workspace, and
	// workspace-lifecycle state — replacing any earlier record for the same
	// Execution/Issue pair.
	RecordExecutionPlacement(ctx context.Context, placement ExecutionPlacement) error

	// ExecutionPlacementByIssue reloads the persisted execution placement
	// for issueID within executionID. Returns ErrNotFound if none has been
	// recorded.
	ExecutionPlacementByIssue(ctx context.Context, executionID, issueID string) (ExecutionPlacement, error)

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

// StatusSignalCheckpoint is the persisted record of whether the ticket-24
// status-reflection start comment (internal/statusreflect) has already been
// posted for one Issue's READY -> CLAIMED transition within an Execution.
// See SaveStatusSignalCheckpoint's doc comment for why the comment, unlike
// the label swap statusreflect.Apply performs, needs a persisted guard.
type StatusSignalCheckpoint struct {
	ExecutionID   string
	IssueID       string
	CommentPosted bool
}

// DecisionCheckpoint is the persisted record of a Planning Execution's
// Decision pausing on NEEDS_HUMAN: the question asked, the Decision
// provenance (DecisionRevision) it arose from, when, and the state of the
// idempotent label/comment side effects. Mirrors NeedsInfoCheckpoint's
// shape and the same crash-window rationale (see
// internal/wayfinding's pause handling) -- CommentAuthor/CommentPostedAt
// are the tracker-reported (server-clock) values, not locally captured,
// for the same clock-skew and self-comment-exclusion reasons.
//
// ResumedAt/ResumedContext are write-only for now, a forward seam for
// ticket 15b's resume handling, mirroring NeedsInfoCheckpoint's identical
// fields.
type DecisionCheckpoint struct {
	ExecutionID      string
	DecisionID       string
	DecisionRevision string
	Question         string
	Context          string
	LabelAdded       bool
	CommentPosted    bool
	CommentAuthor    string
	CommentPostedAt  time.Time
	CreatedAt        time.Time
	ResumedAt        *time.Time
	ResumedContext   string
}
