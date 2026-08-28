// Package engine wires internal/domain, internal/storage, internal/tracker,
// internal/workspace, and internal/agent together into Forge's first
// end-to-end vertical slice (ticket 18): driving one Issue with no unmet
// Dependencies through the orchestration pipeline. See CONTEXT.md
// "Execution", "Worker", "Execution Context".
//
// Engine depends only on the narrow IssueFetcher, WorkspaceCreator, and
// storage.Store/agent.Agent interfaces it actually calls — never on a
// concrete backend (github, claude, sqlite, git). Concrete adapters are
// constructed and injected by cmd/forge, which keeps this package's tests
// hermetic and the orchestration core backend-agnostic.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/repocontext"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
)

// IssueFetcher is the subset of tracker.Tracker's exported behavior the
// engine needs: fetching a single normalized Issue. Depending on this
// interface rather than tracker.Tracker keeps Engine backend-agnostic and
// its test doubles down to one method.
type IssueFetcher interface {
	GetIssue(ctx context.Context, id string) (domain.Issue, error)
}

// WorkspaceCreator is the subset of *workspace.Manager's exported behavior
// the engine needs: creating a Workspace before invoking the Agent, and
// removing it (best-effort) when a run fails after it was created.
// Depending on this interface rather than *workspace.Manager directly keeps
// Engine backend-agnostic and lets tests inject a hermetic double without a
// real git binary.
type WorkspaceCreator interface {
	Create(ctx context.Context, executionID, issueID, base string) (domain.Workspace, error)
	Cleanup(ctx context.Context, executionID, issueID string) error
}

// Engine drives a single Issue through Forge's state machine, persisting
// every transition (and a handful of informational Events) to Store, and
// invoking Agent inside a Workspace built from the compiled Repository
// Context.
type Engine struct {
	Store      storage.Store
	Tracker    IssueFetcher
	Workspaces WorkspaceCreator
	Agent      agent.Agent
	Config     config.Config

	// NeedsInfoTracker is the subset of tracker.Tracker the NEEDS_INFO
	// handling needs (add label, post comment) — see needsinfo.go. It is
	// optional: nil disables the label/comment side effects (e.g. a test
	// double that only implements IssueFetcher), so existing callers of New
	// keep compiling unchanged. cmd/forge wires it to the same tracker
	// instance as Tracker.
	NeedsInfoTracker NeedsInfoTracker

	// RepoRoot is the primary checkout's working directory, used to compile
	// the Repository Context (internal/repocontext) and as the root
	// Workspaces are created under.
	RepoRoot string

	// Now and NewExecutionID are seams for deterministic tests; New sets
	// both to real implementations (time.Now, uuid.NewString).
	//
	// Now governs only Engine's own timestamps (Execution.StartedAt and the
	// informational Events Execute appends directly). It does NOT govern
	// the "issue.transitioned"/"issue.claimed" Events storage.SQLiteStore
	// appends internally — those are stamped with time.Now() inside the
	// storage package, which has no clock seam of its own. A test that
	// injects a fixed/synthetic Now and asserts strict Event ordering by
	// timestamp should be aware storage-side events use the real wall
	// clock regardless (events are still returned in insertion order, so
	// ordering assertions that rely on that rather than on timestamp
	// values are unaffected).
	Now            func() time.Time
	NewExecutionID func() string
}

// New builds an Engine from its injected dependencies.
func New(store storage.Store, trk IssueFetcher, workspaces WorkspaceCreator, ag agent.Agent, cfg config.Config, repoRoot string) *Engine {
	return &Engine{
		Store:          store,
		Tracker:        trk,
		Workspaces:     workspaces,
		Agent:          ag,
		Config:         cfg,
		RepoRoot:       repoRoot,
		Now:            time.Now,
		NewExecutionID: func() string { return uuid.NewString() },
	}
}

// ExecuteResult is the outcome of Execute: the Execution it created and the
// Issue's state once the single-issue pipeline reaches a resting state.
type ExecuteResult struct {
	ExecutionID string
	Issue       domain.Issue
}

// Execute fetches issueID from Tracker — assumed to have no unmet
// Dependencies — and drives it through:
//
//	PENDING -> READY -> CLAIMED -> PREPARING -> IMPLEMENTING
//
// then, depending on the Agent's result:
//
//	IMPLEMENTED  -> VALIDATING (Quality Gates are ticket 19's concern; this
//	                is a resting state, not a fabricated gate pass)
//	NEEDS_INFO   -> NEEDS_INFO
//	FAILED       -> FAILED (a direct, legal edge from IMPLEMENTING)
//
// baseRevision is the Execution's starting base SHA, resolved by the caller
// (cmd/forge) so Engine never shells out to git itself. Every transition is
// persisted via Store.TransitionIssue, which validates it against
// domain.ValidateTransition and appends an "issue.transitioned" Event before
// any write commits.
//
// Once the Workspace has been created, any further error (an Agent error, a
// failed transition, a failed Event append) drives the Issue to a terminal
// state and best-effort removes the now-orphaned Workspace before returning
// — see failOut.
func (e *Engine) Execute(ctx context.Context, issueID, baseRevision string) (ExecuteResult, error) {
	execution := domain.Execution{
		ID:           e.NewExecutionID(),
		BaseRevision: baseRevision,
		StartedAt:    e.Now(),
	}
	if err := e.Store.CreateExecution(ctx, execution); err != nil {
		return ExecuteResult{}, fmt.Errorf("engine: create execution: %w", err)
	}

	issue, err := e.Tracker.GetIssue(ctx, issueID)
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("engine: fetch issue %s: %w", issueID, err)
	}
	// Tracker adapters normalize only tracker-native fields (ID,
	// Dependencies); Scope, State, and RetryBudget are execution-set
	// concerns the engine owns (see internal/tracker/github's normalizeIssue
	// comment: "leave it at its zero value here so there is exactly one
	// writer of the field").
	issue.ExecutionID = execution.ID
	issue.State = domain.StatePending
	issue.Scope = domain.ScopeManaged
	issue.RetryBudget = domain.NewRetryBudget(e.Config.Retry)

	// A single Issue with no Dependencies trivially has no cycle, but ticket
	// 18 requires the check run regardless — it's the same code path
	// multi-issue Executions (ticket 26) will rely on.
	if _, err := tracker.BuildDAG([]domain.Issue{issue}); err != nil {
		return ExecuteResult{}, fmt.Errorf("engine: dependency graph for issue %s: %w", issueID, err)
	}

	if err := e.Store.CreateIssue(ctx, issue); err != nil {
		return ExecuteResult{}, fmt.Errorf("engine: create issue %s: %w", issueID, err)
	}

	// Repository Context is compiled exactly once per Execution (CONTEXT.md
	// "Repository Context") and handed to every Worker unchanged.
	repoCtx, err := repocontext.Compile(e.Config, e.RepoRoot, baseRevision)
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("engine: compile repository context: %w", err)
	}

	issue, err = e.transition(ctx, execution.ID, issueID, domain.StateReady)
	if err != nil {
		return ExecuteResult{}, err
	}
	// The Worker base is captured at the READY transition (CONTEXT.md
	// "Execution"): a dependency-blocked Issue's Worker would capture a
	// newer base once its prerequisites merge. Ticket 18 only handles a
	// single Issue with no Dependencies, so its Worker base is always the
	// Execution's starting base.
	workerBase := baseRevision
	if err := e.appendEvent(ctx, execution.ID, issueID, "worker.base_captured", map[string]string{
		"base": workerBase,
	}); err != nil {
		return ExecuteResult{}, err
	}

	workerRef := fmt.Sprintf("worker-%s-%s", execution.ID, issueID)
	if err := e.Store.ClaimIssue(ctx, execution.ID, issueID, workerRef); err != nil {
		return ExecuteResult{}, fmt.Errorf("engine: claim issue %s: %w", issueID, err)
	}
	issue, err = e.transition(ctx, execution.ID, issueID, domain.StateClaimed)
	if err != nil {
		return ExecuteResult{}, err
	}

	issue, err = e.transition(ctx, execution.ID, issueID, domain.StatePreparing)
	if err != nil {
		return ExecuteResult{}, err
	}

	// The Workspace is created before the Agent is invoked, while still in
	// PREPARING. From here on, any error path must clean the Workspace up
	// and drive the Issue to a terminal state via failOut.
	ws, err := e.Workspaces.Create(ctx, execution.ID, issueID, workerBase)
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("engine: create workspace for issue %s: %w", issueID, err)
	}
	if err := e.appendEvent(ctx, execution.ID, issueID, "workspace.created", map[string]string{
		"path":   ws.Path,
		"branch": ws.Branch,
	}); err != nil {
		return ExecuteResult{}, e.failOut(ctx, execution.ID, issueID, err)
	}

	issue, err = e.transition(ctx, execution.ID, issueID, domain.StateImplementing)
	if err != nil {
		return ExecuteResult{}, e.failOut(ctx, execution.ID, issueID, err)
	}

	// Execution Context (CONTEXT.md "Execution Context") is assembled here:
	// the compiled Repository Context plus this Worker's Issue-specific
	// data (Workspace path, normalized Issue, workflow policy). Feedback is
	// empty on this first attempt — repair loops are ticket 21/24.
	req := agent.AgentRequest{
		WorkspacePath: ws.Path,
		Issue:         issue,
		Repository:    repoCtx,
		Policy:        agent.WorkflowPolicy{},
	}
	result, err := e.Agent.Execute(ctx, req)
	if err != nil {
		return ExecuteResult{}, e.failOut(ctx, execution.ID, issueID, fmt.Errorf("engine: agent execute issue %s: %w", issueID, err))
	}
	if err := e.appendEvent(ctx, execution.ID, issueID, "agent.result", map[string]string{
		"status":  string(result.Status),
		"summary": result.Summary,
	}); err != nil {
		return ExecuteResult{}, e.failOut(ctx, execution.ID, issueID, err)
	}

	switch result.Status {
	case agent.StatusImplemented:
		issue, err = e.transition(ctx, execution.ID, issueID, domain.StateValidating)
	case agent.StatusNeedsInfo:
		issue, err = e.handleNeedsInfo(ctx, execution.ID, issueID, workerRef, result)
	case agent.StatusFailed:
		issue, err = e.transition(ctx, execution.ID, issueID, domain.StateFailed)
	default:
		err = fmt.Errorf("engine: agent returned unknown status %q for issue %s", result.Status, issueID)
	}
	if err != nil {
		return ExecuteResult{}, e.failOut(ctx, execution.ID, issueID, err)
	}

	return ExecuteResult{ExecutionID: execution.ID, Issue: issue}, nil
}

// transition moves issueID to state `to` via Store.TransitionIssue, wrapping
// any error (including *domain.InvalidTransitionError, unwrappable via
// errors.As) with the operation's context.
func (e *Engine) transition(ctx context.Context, executionID, issueID string, to domain.IssueState) (domain.Issue, error) {
	issue, err := e.Store.TransitionIssue(ctx, executionID, issueID, to)
	if err != nil {
		return domain.Issue{}, fmt.Errorf("engine: transition issue %s to %s: %w", issueID, to, err)
	}
	return issue, nil
}

// appendEvent records an informational Event (distinct from the
// "issue.transitioned"/"issue.claimed" Events Store appends automatically)
// so `forge status` can show workspace creation and the Agent's result
// alongside state transitions.
func (e *Engine) appendEvent(ctx context.Context, executionID, issueID, eventType string, data map[string]string) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("engine: marshal event %s: %w", eventType, err)
	}
	if err := e.Store.AppendEvent(ctx, storage.Event{
		ExecutionID: executionID,
		IssueID:     issueID,
		Type:        eventType,
		Data:        string(payload),
		OccurredAt:  e.Now(),
	}); err != nil {
		return fmt.Errorf("engine: append event %s: %w", eventType, err)
	}
	return nil
}

// failOut is called on every error path once the Workspace has been
// created. It best-effort removes the now-orphaned Workspace (Execute mints
// a fresh Execution ID per call, so a leaked worktree accumulates one per
// failed run if this is skipped) and drives the Issue to a terminal state,
// without masking origErr — the cleanup error, if any, is joined onto it
// via errors.Join rather than swallowed or replacing it.
//
// The Issue is routed to FAILED where that is a legal transition (from
// IMPLEMENTING, per domain.state.go). For an error occurring before
// IMPLEMENTING is reached (e.g. appending the "workspace.created" Event
// fails while still in PREPARING, which has no FAILED edge), it falls back
// to CANCELLED, which domain.ValidateTransition permits from any
// non-terminal state — an infra failure before the Agent ever ran is better
// described as an aborted run than a failed one.
func (e *Engine) failOut(ctx context.Context, executionID, issueID string, origErr error) error {
	errs := []error{origErr}

	if err := e.Workspaces.Cleanup(ctx, executionID, issueID); err != nil {
		errs = append(errs, fmt.Errorf("engine: cleanup workspace for issue %s: %w", issueID, err))
	}

	if _, err := e.Store.TransitionIssue(ctx, executionID, issueID, domain.StateFailed); err != nil {
		if _, err := e.Store.TransitionIssue(ctx, executionID, issueID, domain.StateCancelled); err != nil {
			errs = append(errs, fmt.Errorf("engine: drive issue %s to a terminal state: %w", issueID, err))
		}
	}

	return errors.Join(errs...)
}
