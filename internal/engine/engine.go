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
	"github.com/Teagan42/forge/internal/gate"
	"github.com/Teagan42/forge/internal/repocontext"
	"github.com/Teagan42/forge/internal/review"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
)

// DiffProducer produces the diff (base...HEAD) for a Workspace, used to
// build a review.Request. Engine stays git-free per its design (see this
// file's package doc comment): a DiffProducer seam lets cmd/forge implement
// this with git while tests inject a fake, exactly as WorkspaceCreator does
// for Workspace creation.
type DiffProducer interface {
	Diff(ctx context.Context, workspacePath, base string) (string, error)
}

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

	// Gates is the CommandRunner the Gate Runner (ticket 19,
	// internal/gate) executes each configured Quality Gate's command
	// through, once an Issue reaches IMPLEMENTED. New defaults it to
	// gate.ExecCommandRunner{}, the real subprocess runner; tests inject a
	// fake so they never shell out.
	Gates gate.CommandRunner

	// Reviewer is the review.Reviewer the REVIEWING stage invokes once
	// Quality Gates pass (ticket 20, CONTEXT.md "Review"). It is optional
	// like NeedsInfoTracker: nil leaves REVIEWING a resting state (this
	// ticket's predecessor behavior, and today's behavior for any caller
	// that has not wired a production Reviewer yet), so existing callers of
	// New keep compiling and behaving unchanged. cmd/forge wires it once a
	// production Reviewer exists.
	Reviewer review.Reviewer

	// Diff is the DiffProducer the REVIEWING stage uses to build the
	// review.Request's diff. Required only when Reviewer is set; see
	// runReview.
	Diff DiffProducer

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
		Gates:          gate.ExecCommandRunner{},
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
//	IMPLEMENTED  -> VALIDATING -> (Quality Gates run: see runQualityGates)
//	                -> REVIEWING -> (Review runs: see runReview)
//	                   -> COMMITTING (APPROVED; a resting state — ticket 22
//	                      owns the actual commit/PR) or -> IMPLEMENTING
//	                      (CHANGES_REQUIRED, Findings persisted — the full
//	                      retry loop that would re-invoke the Agent with
//	                      them as Feedback is ticket 21's concern)
//	                or -> FAILED (first Quality Gate failure; the retry
//	                   loop that would route this back to IMPLEMENTING
//	                   instead is ticket 21's concern)
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
		if err != nil {
			return ExecuteResult{}, e.failOut(ctx, execution.ID, issueID, err)
		}
		var gatesPassed bool
		var gateResults []gate.Result
		issue, gatesPassed, gateResults, err = e.runQualityGates(ctx, execution.ID, issueID, ws.Path, issue)
		if err == nil && gatesPassed {
			issue, err = e.runReview(ctx, execution.ID, issueID, workerBase, ws.Path, repoCtx, issue, gateResults)
		}
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

// runQualityGates runs the Issue's configured Quality Gates (ticket 19,
// internal/gate) for an Issue already in VALIDATING, persisting each
// Result via Store.RecordGateRun (the full, bounded stdout/stderr live
// there). All gates passing transitions the Issue to REVIEWING and returns
// every passing Result (so runReview can hand them to the Reviewer as
// gate-results context, per CONTEXT.md "Review"); the first failure
// (Options{} defaults to stop-on-first-fail) records a lean "gate.failed"
// diagnostic Event (name/command/exit_code — the Event log stays an audit
// trail, not a duplicate copy of gate_runs) and transitions the Issue to
// FAILED, with a nil Result slice. Building agent.Feedback
// (gate.BuildFeedback) from the failing GateRun and routing it back to
// IMPLEMENTING under a retry budget is ticket 21's concern — this is
// deliberately a single-pass gate check.
//
// The returned passed bool is Execute's explicit "did gates pass" signal —
// callers should branch on it directly rather than re-deriving the same
// fact by comparing the returned Issue's State against
// domain.StateReviewing.
func (e *Engine) runQualityGates(ctx context.Context, executionID, issueID, workspacePath string, issue domain.Issue) (_ domain.Issue, passed bool, _ []gate.Result, _ error) {
	runner := gate.NewRunner(e.Gates)
	results := runner.Run(ctx, workspacePath, e.Config.Quality.Gates, gate.Options{
		MaxOutputBytes: e.Config.Quality.MaxOutputBytes,
	})

	var failed *gate.Result
	for i := range results {
		res := results[i]
		if err := e.Store.RecordGateRun(ctx, storage.GateRun{
			ExecutionID: executionID,
			IssueID:     issueID,
			Name:        res.Name,
			Command:     res.Command,
			StartedAt:   res.StartedAt,
			FinishedAt:  res.FinishedAt,
			ExitCode:    res.ExitCode,
			Stdout:      res.Stdout,
			Stderr:      res.Stderr,
			Passed:      res.Passed,
		}); err != nil {
			return domain.Issue{}, false, nil, fmt.Errorf("engine: record gate run %s for issue %s: %w", res.Name, issueID, err)
		}
		if !res.Passed && failed == nil {
			failed = &results[i]
		}
	}

	if failed == nil {
		issue, err := e.transition(ctx, executionID, issueID, domain.StateReviewing)
		return issue, true, results, err
	}

	// The "gate.failed" Event stays lean (name/command/exit_code, matching
	// "gate.run") rather than duplicating the failing gate's full captured
	// output, which already lives in the persisted GateRun row. Ticket 21's
	// retry loop rebuilds agent.Feedback (via gate.BuildFeedback) fresh from
	// that row rather than from this Event.
	if err := e.appendEvent(ctx, executionID, issueID, "gate.failed", map[string]string{
		"name":      failed.Name,
		"command":   failed.Command,
		"exit_code": fmt.Sprint(failed.ExitCode),
	}); err != nil {
		return domain.Issue{}, false, nil, err
	}

	issue, err := e.transition(ctx, executionID, issueID, domain.StateFailed)
	return issue, false, nil, err
}

// runReview implements REVIEWING once Quality Gates have passed (ticket 20,
// CONTEXT.md "Review"): a fresh review.Reviewer invocation — never the
// implementation Agent's prior conversation — receiving the diff (produced
// by the injected DiffProducer, since Engine stays git-free), the Issue,
// the Repository Context, and gateResults. When Reviewer is unset (the
// optional-seam convention shared with NeedsInfoTracker/Gates — see
// Engine.Reviewer's doc comment), REVIEWING stays a resting state, matching
// this ticket's predecessor behavior.
//
// review.VerdictApproved transitions the Issue to COMMITTING (ticket 22
// owns the actual commit/PR — this is deliberately another resting state
// for now). review.VerdictChangesRequired persists the review run and its
// Findings, and transitions the Issue back to IMPLEMENTING; converting
// those Findings into agent.Feedback for a repair re-invocation
// (review.BuildFeedback) and driving the full retry-budget loop is ticket
// 21's concern — this ticket's scope stops at the single
// CHANGES_REQUIRED -> IMPLEMENTING transition with Findings
// persisted/available.
//
// The diff itself only reflects committed work (see cmd/forge's
// gitDiffProducer doc comment): until ticket 22's commit step exists, a
// production Reviewer wired up here would see an empty diff regardless of
// what the Agent changed on disk.
func (e *Engine) runReview(ctx context.Context, executionID, issueID, workerBase, workspacePath string, repoCtx agent.RepositoryContext, issue domain.Issue, gateResults []gate.Result) (domain.Issue, error) {
	if e.Reviewer == nil {
		return issue, nil
	}
	if e.Diff == nil {
		return domain.Issue{}, fmt.Errorf("engine: Reviewer is set but Diff (DiffProducer) is nil for issue %s", issueID)
	}

	diff, err := e.Diff.Diff(ctx, workspacePath, workerBase)
	if err != nil {
		return domain.Issue{}, fmt.Errorf("engine: produce diff for issue %s: %w", issueID, err)
	}

	started := e.Now()
	result, err := e.Reviewer.Review(ctx, review.Request{
		Diff:        diff,
		Issue:       issue,
		Repository:  repoCtx,
		GateResults: gateResults,
	})
	if err != nil {
		return domain.Issue{}, fmt.Errorf("engine: reviewer execute issue %s: %w", issueID, err)
	}
	finished := e.Now()

	findings := make([]storage.ReviewFinding, len(result.Findings))
	for i, f := range result.Findings {
		findings[i] = storage.ReviewFinding{
			Severity: string(f.Severity),
			File:     f.File,
			Line:     f.Line,
			Message:  f.Message,
		}
	}
	if err := e.Store.RecordReviewRun(ctx, storage.ReviewRun{
		ExecutionID: executionID,
		IssueID:     issueID,
		Verdict:     string(result.Verdict),
		Summary:     result.Summary,
		Diff:        diff,
		StartedAt:   started,
		FinishedAt:  finished,
		Findings:    findings,
	}); err != nil {
		return domain.Issue{}, fmt.Errorf("engine: record review run for issue %s: %w", issueID, err)
	}

	switch result.Verdict {
	case review.VerdictApproved:
		return e.transition(ctx, executionID, issueID, domain.StateCommitting)
	case review.VerdictChangesRequired:
		return e.transition(ctx, executionID, issueID, domain.StateImplementing)
	default:
		return domain.Issue{}, fmt.Errorf("engine: reviewer returned unknown verdict %q for issue %s", result.Verdict, issueID)
	}
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
