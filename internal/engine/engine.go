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
	"os"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/gate"
	"github.com/Teagan42/forge/internal/repocontext"
	"github.com/Teagan42/forge/internal/review"
	"github.com/Teagan42/forge/internal/statusreflect"
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
	Validate(ctx context.Context, executionID, issueID string) (domain.Workspace, error)
}

// WorkspaceRebaser is an optional capability of a WorkspaceCreator: moving
// an existing Workspace's branch onto a new base in place, instead of the
// destroy-and-recreate Cleanup+Create sequence WorkspaceCreator alone would
// require. RetryIssue type-asserts for it when refreshing a retried Issue's
// Worker base (ticket 29) onto a Workspace that already exists.
//
// A conflict-free rebase returns (nil, nil). A rebase that hits a conflict
// is aborted (the Workspace is left exactly as it was) and its conflicting
// paths are returned with a nil error — a conflict is an expected,
// caller-actionable outcome, not an infrastructure failure.
type WorkspaceRebaser interface {
	Rebase(ctx context.Context, executionID, issueID, newBase string) (conflictPaths []string, err error)
}

// TargetTipResolver resolves the current tip of the target branch Workers
// open pull requests against. RetryIssue uses it to refresh a retried
// Issue's Worker base forward to that tip (ticket 29) instead of reusing
// the base captured at the Issue's original READY transition (ADR 0006).
// Optional: nil leaves RetryIssue's pre-ticket-29 behavior (reuse the
// recorded base unchanged) in place, so existing callers of New keep
// compiling and behaving unchanged.
type TargetTipResolver interface {
	CurrentTip(ctx context.Context) (string, error)
}

// TargetTipResolverFunc adapts a plain function to a TargetTipResolver.
type TargetTipResolverFunc func(ctx context.Context) (string, error)

func (f TargetTipResolverFunc) CurrentTip(ctx context.Context) (string, error) {
	return f(ctx)
}

// AncestorChecker reports whether commit is an ancestor of (reachable
// from) branch's current tip. RetryIssue uses it to refuse a base refresh
// that would move to a tip not descended from the previously captured
// base — preserving the "never branch from a base that predates a
// dependency's merge" invariant (ADR 0005/0006) when TargetTip is wired.
// Optional like TargetTip: nil skips the check, trusting TargetTip to
// always resolve forward.
type AncestorChecker interface {
	IsAncestor(ctx context.Context, commit, branch string) (bool, error)
}

// CIWaiter resumes CI supervision for an Issue already in CI_PENDING.
type CIWaiter interface {
	Wait(ctx context.Context, executionID, issueID string) (domain.IssueState, error)
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

	// Publisher commits and pushes validated work once Review approves it
	// (ticket 22, CONTEXT.md "COMMITTING"). It is optional like Reviewer:
	// nil leaves COMMITTING a resting state (this ticket's predecessor
	// behavior), so existing callers of New keep compiling and behaving
	// unchanged. cmd/forge wires it to a production git-backed Publisher.
	// See runCommitAndPR.
	Publisher Publisher

	// PRTracker is the subset of tracker.Tracker the PR_CREATING stage uses
	// to idempotently create (or recover) a pull request (ticket 22). It is
	// optional like NeedsInfoTracker/Publisher: nil leaves COMMITTING a
	// resting state — see runCommitAndPR, which treats Publisher and
	// PRTracker as a single all-or-nothing seam.
	PRTracker PRCreator

	// CIWaiter resumes CI monitoring for issues already in CI_PENDING.
	CIWaiter CIWaiter

	// TargetTip resolves the target branch's current tip so RetryIssue can
	// refresh a retried Issue's Worker base forward to it (ticket 29).
	// Optional: nil preserves pre-ticket-29 behavior of reusing the base
	// recorded at the Issue's original READY transition.
	TargetTip TargetTipResolver

	// Ancestry checks that a refreshed base still descends from the
	// previously captured one before RetryIssue applies it (ticket 29).
	// Optional: see TargetTip.
	Ancestry AncestorChecker

	// StatusTracker is the subset of tracker.Tracker the status-reflection
	// signal (ticket 24, internal/statusreflect) uses to add/remove labels
	// and post a start comment as an Issue moves through active work. It is
	// optional like NeedsInfoTracker/Reviewer/Publisher: nil (or
	// Config.StatusReflection.Enabled false, the default) leaves every
	// transition's tracker side effect a no-op — see transition, which
	// calls statusreflect.Apply after every persisted state change.
	StatusTracker statusreflect.Tracker

	// BaseBranch is the plain branch name (e.g. "main") pull requests
	// target, distinct from the base revision Workers resolve to a commit
	// SHA. Engine has no notion of git remotes, so this is resolved by
	// cmd/forge (e.g. stripping a "origin/" prefix from cfg.Git.Base)
	// rather than derived here. Required only when Publisher/PRTracker are
	// set; see runCommitAndPR.
	BaseBranch string

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
	Now                func() time.Time
	NewExecutionID     func() string
	OwnerPID           func() int
	ProcessRunning     func(pid int) (bool, error)
	InterruptProcess   func(pid int) error
	WaitForProcessExit func(ctx context.Context, pid int) error
}

// New builds an Engine from its injected dependencies.
func New(store storage.Store, trk IssueFetcher, workspaces WorkspaceCreator, ag agent.Agent, cfg config.Config, repoRoot string) *Engine {
	return &Engine{
		Store:              store,
		Tracker:            trk,
		Workspaces:         workspaces,
		Agent:              ag,
		Config:             cfg,
		Gates:              gate.ExecCommandRunner{},
		RepoRoot:           repoRoot,
		Now:                time.Now,
		NewExecutionID:     func() string { return uuid.NewString() },
		OwnerPID:           os.Getpid,
		ProcessRunning:     processRunning,
		InterruptProcess:   interruptProcess,
		WaitForProcessExit: waitForProcessExit,
	}
}

func interruptProcess(pid int) error {
	if pid <= 0 {
		return nil
	}
	return syscall.Kill(pid, syscall.SIGINT)
}

func waitForProcessExit(ctx context.Context, pid int) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()

	for {
		running, err := processRunning(pid)
		if err != nil {
			return err
		}
		if !running {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout.C:
			return fmt.Errorf("process %d still running after cancellation timeout", pid)
		case <-ticker.C:
		}
	}
}

func processRunning(pid int) (bool, error) {
	if pid <= 0 {
		return false, nil
	}
	err := syscall.Kill(pid, 0)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, syscall.ESRCH):
		return false, nil
	case errors.Is(err, syscall.EPERM):
		return true, nil
	default:
		return false, err
	}
}

// ExecuteResult is the outcome of Execute: the Execution it created and the
// Issue's state once the single-issue pipeline reaches a resting state.
type ExecuteResult struct {
	ExecutionID string
	Issue       domain.Issue
}

// StartExecution persists a new Execution rooted at baseRevision and
// returns it. Single-issue Execute uses this directly; multi-issue
// scheduling can call it once, then dispatch multiple Issues through
// ExecuteInExecution.
func (e *Engine) StartExecution(ctx context.Context, baseRevision string) (domain.Execution, error) {
	execution := domain.Execution{
		ID:           e.NewExecutionID(),
		BaseRevision: baseRevision,
		StartedAt:    e.Now(),
	}
	if err := e.Store.CreateExecution(ctx, execution); err != nil {
		return domain.Execution{}, fmt.Errorf("engine: create execution: %w", err)
	}
	return execution, nil
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
//	                   -> COMMITTING (APPROVED)
//	                      -> PR_CREATING -> CI_PENDING (commit, push, and
//	                         PR creation/recovery: see runCommitAndPR —
//	                         COMMITTING stays a resting state when
//	                         Publisher/PRTracker are unset, ticket 22's
//	                         predecessor behavior)
//	                or -> FAILED (Quality Gate failure or CHANGES_REQUIRED
//	                   review verdict once its retry budget is exhausted)
//
// A Quality Gate failure or a CHANGES_REQUIRED review verdict does not fail
// the Issue outright: runRepairLoop (CONTEXT.md "Retry Budget") routes it
// back to IMPLEMENTING, re-invoking the Agent with only the new bounded
// diagnostic/findings (gate.BuildFeedback / review.BuildFeedback — never a
// full-history replay), then reruns the complete configured Quality Gate
// set (and, once that passes again, Review) before deciding again. Gate
// failures and review rejections draw from independent RetryBudget
// counters (ticket 21); either counter's exhaustion transitions the Issue
// to FAILED with its diagnostics preserved. The Workspace is never
// recreated between repair attempts — the same Workspace built once before
// IMPLEMENTING is reused for every iteration.
//
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
	execution, err := e.StartExecution(ctx, baseRevision)
	if err != nil {
		return ExecuteResult{}, err
	}
	return e.ExecuteInExecution(ctx, execution, issueID, baseRevision)
}

// ExecuteInExecution drives one Issue through the execution pipeline inside
// an already-created Execution. workerBase is the per-Worker base captured
// at READY; it may differ from execution.BaseRevision for dependency-
// blocked Issues that become ready later in a shared multi-Issue run.
func (e *Engine) ExecuteInExecution(ctx context.Context, execution domain.Execution, issueID, workerBase string) (_ ExecuteResult, retErr error) {
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
	repoCtx, err := repocontext.Compile(e.Config, e.RepoRoot, workerBase)
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
	if err := e.appendEvent(ctx, execution.ID, issueID, "worker.base_captured", map[string]string{
		"base": workerBase,
	}); err != nil {
		return ExecuteResult{}, err
	}

	ref := workerRef(execution.ID, issueID)
	if err := e.Store.ClaimIssue(ctx, execution.ID, issueID, ref); err != nil {
		return ExecuteResult{}, fmt.Errorf("engine: claim issue %s: %w", issueID, err)
	}
	defer func() {
		if err := e.Store.ReleaseWorkerClaim(ctx, execution.ID, issueID); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("engine: release worker claim for issue %s: %w", issueID, err))
		}
	}()
	if err := e.Store.UpdateWorkerOwner(ctx, execution.ID, issueID, e.OwnerPID()); err != nil {
		return ExecuteResult{}, fmt.Errorf("engine: record worker owner for issue %s: %w", issueID, err)
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
	if err := e.Store.RecordWorkspace(ctx, execution.ID, ws); err != nil {
		return ExecuteResult{}, fmt.Errorf("engine: persist workspace for issue %s: %w", issueID, err)
	}
	if err := e.appendEvent(ctx, execution.ID, issueID, "workspace.created", map[string]string{
		"path":   ws.Path,
		"branch": ws.Branch,
	}); err != nil {
		return ExecuteResult{}, e.failOut(ctx, execution.ID, issueID, err)
	}

	// Execution Context (CONTEXT.md "Execution Context") is assembled inside
	// invokeAgent: the compiled Repository Context plus this Worker's
	// Issue-specific data (Workspace path, normalized Issue, workflow
	// policy). Feedback is nil on this first attempt — invokeAgent also
	// owns the PREPARING -> IMPLEMENTING transition, so this call is the
	// same one runRepairLoop's repair iterations make (see invokeAgent),
	// just with no prior diagnostic to report.
	var implemented bool
	issue, implemented, err = e.invokeAgent(ctx, execution.ID, issueID, ws.Path, repoCtx, issue, nil)
	if err != nil {
		return ExecuteResult{}, e.failOut(ctx, execution.ID, issueID, err)
	}

	if implemented {
		issue, err = e.runRepairLoop(ctx, execution.ID, issueID, workerBase, ws.Path, repoCtx, issue)
		if err != nil {
			return ExecuteResult{}, e.failOut(ctx, execution.ID, issueID, err)
		}
		if issue.State == domain.StateCommitting {
			issue, err = e.runCommitAndPR(ctx, execution.ID, issueID, workerBase, ws, issue)
			if err != nil {
				return ExecuteResult{}, e.failOut(ctx, execution.ID, issueID, err)
			}
		}
	}
	// A non-implemented, non-error outcome (NEEDS_INFO or FAILED) is already
	// a resting state driven there by invokeAgent itself — nothing further
	// to do.

	return ExecuteResult{ExecutionID: execution.ID, Issue: issue}, nil
}

// RepairCIFailure resumes an Issue already parked in CI_FAILED: it reloads
// the persisted Execution/Issue state, validates the existing Workspace,
// rebuilds the latest failed CI diagnostic into bounded Agent feedback,
// decrements the independent CI retry budget, and re-enters the existing
// implementation -> validate -> review -> commit/push flow in place.
func (e *Engine) RepairCIFailure(ctx context.Context, executionID, issueID string) (domain.Issue, error) {
	state, err := e.Store.LoadExecution(ctx, executionID)
	if err != nil {
		return domain.Issue{}, fmt.Errorf("engine: load execution %s: %w", executionID, err)
	}

	issue, err := e.Store.GetIssue(ctx, executionID, issueID)
	if err != nil {
		return domain.Issue{}, fmt.Errorf("engine: load issue %s: %w", issueID, err)
	}
	if issue.State != domain.StateCIFailed {
		return domain.Issue{}, fmt.Errorf("engine: issue %s is %s, want CI_FAILED", issueID, issue.State)
	}

	ws, err := e.Workspaces.Validate(ctx, executionID, issueID)
	if err != nil {
		return domain.Issue{}, fmt.Errorf("engine: validate workspace for issue %s: %w", issueID, err)
	}

	workerBase, err := e.workerBase(ctx, state.Execution, issueID)
	if err != nil {
		return domain.Issue{}, err
	}

	repoCtx, err := repocontext.Compile(e.Config, e.RepoRoot, workerBase)
	if err != nil {
		return domain.Issue{}, fmt.Errorf("engine: compile repository context: %w", err)
	}

	feedback, err := e.latestCIFeedback(ctx, executionID, issueID)
	if err != nil {
		return domain.Issue{}, err
	}

	issue, retried, err := e.repair(
		ctx, executionID, issueID, ws.Path, repoCtx, issue,
		issue.RetryBudget.CIExhausted(), (*domain.Issue).RecordCIFailure, []agent.Feedback{feedback}, "ci",
	)
	if err != nil {
		return domain.Issue{}, err
	}
	if !retried {
		return issue, nil
	}

	issue, err = e.runRepairLoop(ctx, executionID, issueID, workerBase, ws.Path, repoCtx, issue)
	if err != nil {
		return domain.Issue{}, err
	}
	if issue.State != domain.StateCommitting {
		return issue, nil
	}
	return e.runCommitAndPR(ctx, executionID, issueID, workerBase, ws, issue)
}

// workerRef derives the Worker identity used both to claim an Issue
// (Store.ClaimIssue) and to release its slot on a NEEDS_INFO outcome
// (handleNeedsInfo's "worker.released" Event) — one source for the shape
// rather than the executionID/issueID concatenation being hand-rebuilt at
// each call site.
func workerRef(executionID, issueID string) string {
	return fmt.Sprintf("worker-%s-%s", executionID, issueID)
}

// runRepairLoop drives an Issue already IMPLEMENTED (the Agent's first
// attempt has already run and returned StatusImplemented — invokeAgent
// reported that via its implemented bool, and Execute calls this next)
// through VALIDATING and REVIEWING, repeatedly, until it reaches a resting
// state: COMMITTING (Review approved, or no Reviewer configured), REVIEWING
// (no Reviewer configured — the ticket-20 predecessor resting state), or
// FAILED (a Quality Gate failure or CHANGES_REQUIRED review verdict once
// its retry budget is exhausted).
//
// Each iteration: run the full configured Quality Gate set (runQualityGates
// — the "Every repair reruns the complete gate set" requirement holds
// because a repair always re-enters this loop from the top, never resuming
// mid-way through Review only). A gate failure or CHANGES_REQUIRED verdict
// consults the Issue's RetryBudget (CONTEXT.md "Retry Budget", independent
// gate/review counters) via repair: budget remaining records the failure,
// persists it, and repairs (invokeAgent) with only that iteration's bounded
// diagnostic before looping back to VALIDATING; budget exhausted
// transitions straight to FAILED instead. The Workspace is the same one
// Execute created before IMPLEMENTING and is never recreated or cleaned up
// between iterations — repair works in place.
func (e *Engine) runRepairLoop(ctx context.Context, executionID, issueID, workerBase, workspacePath string, repoCtx agent.RepositoryContext, issue domain.Issue) (domain.Issue, error) {
	for {
		issue, err := e.transition(ctx, executionID, issueID, domain.StateValidating)
		if err != nil {
			return domain.Issue{}, err
		}

		issue, gatesPassed, gateResults, failedGate, err := e.runQualityGates(ctx, executionID, issueID, workspacePath, issue)
		if err != nil {
			return domain.Issue{}, err
		}

		if !gatesPassed {
			feedback := []agent.Feedback{gate.BuildFeedback(*failedGate)}
			issue, retried, err := e.repair(ctx, executionID, issueID, workspacePath, repoCtx, issue,
				issue.RetryBudget.GateExhausted(), (*domain.Issue).RecordGateFailure, feedback, "gate")
			if err != nil {
				return domain.Issue{}, err
			}
			if !retried {
				// Budget exhausted (FAILED) or the repair Agent invocation
				// itself ended the run (NEEDS_INFO/FAILED) — either way this
				// is a resting state, not another loop iteration.
				return issue, nil
			}
			continue
		}

		// runReview itself short-circuits on a nil Reviewer (returning
		// VerdictApproved, the ticket-20 predecessor resting state), so
		// there is no separate nil check needed here.
		issue, verdict, findings, err := e.runReview(ctx, executionID, issueID, workerBase, workspacePath, repoCtx, issue, gateResults)
		if err != nil {
			return domain.Issue{}, err
		}
		if verdict != review.VerdictChangesRequired {
			return issue, nil
		}

		feedback := review.BuildFeedback(findings)
		issue, retried, err := e.repair(ctx, executionID, issueID, workspacePath, repoCtx, issue,
			issue.RetryBudget.ReviewExhausted(), (*domain.Issue).RecordReviewRejection, feedback, "review")
		if err != nil {
			return domain.Issue{}, err
		}
		if !retried {
			return issue, nil
		}
	}
}

// repair implements the Retry Budget's (CONTEXT.md) shared shape for both
// the gate and review repair paths — the two differ only in which
// RetryBudget counter they consult/record and which bounded Feedback they
// hand the Agent. exhausted is the caller's already-evaluated
// GateExhausted()/ReviewExhausted() check; record is the corresponding
// pointer-receiver mutator (domain.Issue.RecordGateFailure or
// .RecordReviewRejection, passed as a method expression so it mutates the
// addressable local issue this function holds, not a copy — see
// RecordGateFailure's doc comment for why that matters); what names the
// class purely for error messages. A future CI retry budget (ticket 24)
// becomes a one-line call to this same helper rather than a third copy.
//
// exhausted transitions the Issue straight to FAILED and returns
// retried=false. Otherwise it records the failure in memory, persists it
// (Store.UpdateRetryBudget — TransitionIssue always reloads the Issue
// fresh, so an unpersisted in-memory increment would otherwise be silently
// discarded by the very next transition; this persist and the following
// invokeAgent transition are two separate writes, safe today since nothing
// resumes mid-repair, but worth folding into one transaction if a
// resume-through-Execute path is ever added — see ticket 31/restart
// recovery), and delegates to invokeAgent, returning its (issue,
// implemented, error) directly: implemented is exactly this function's
// retried.
func (e *Engine) repair(ctx context.Context, executionID, issueID, workspacePath string, repoCtx agent.RepositoryContext, issue domain.Issue, exhausted bool, record func(*domain.Issue) error, feedback []agent.Feedback, what string) (_ domain.Issue, retried bool, _ error) {
	if exhausted {
		issue, err := e.transition(ctx, executionID, issueID, domain.StateFailed)
		return issue, false, err
	}

	if err := record(&issue); err != nil {
		return domain.Issue{}, false, fmt.Errorf("engine: record %s failure for issue %s: %w", what, issueID, err)
	}
	if err := e.Store.UpdateRetryBudget(ctx, executionID, issueID, issue.RetryBudget); err != nil {
		return domain.Issue{}, false, fmt.Errorf("engine: persist retry budget for issue %s: %w", issueID, err)
	}

	return e.invokeAgent(ctx, executionID, issueID, workspacePath, repoCtx, issue, feedback)
}

func (e *Engine) latestCIFeedback(ctx context.Context, executionID, issueID string) (agent.Feedback, error) {
	runs, err := e.Store.CIRunsByIssue(ctx, executionID, issueID)
	if err != nil {
		return agent.Feedback{}, fmt.Errorf("engine: load ci runs for issue %s: %w", issueID, err)
	}
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].Status == storage.CIRunStatusFailed {
			return buildCIFeedback(runs[i]), nil
		}
	}
	return agent.Feedback{}, fmt.Errorf("engine: issue %s has no failed CI run to repair", issueID)
}

func buildCIFeedback(run storage.CIRun) agent.Feedback {
	message := fmt.Sprintf("CI check failed:\nCheck: %s", run.CheckName)
	if run.Details != "" {
		message += "\nDetails:\n" + run.Details
	}
	return agent.Feedback{Source: agent.FeedbackSourceCI, Message: message}
}

// invokeAgent transitions issue to IMPLEMENTING and invokes the Agent with
// feedback as its AgentRequest.Feedback — nil on Execute's first attempt,
// bounded to the current repair iteration's diagnostic on every subsequent
// call from repair, never a full-history replay. Both Execute's first
// attempt and every repair iteration route through this one method, so a
// StatusFailed/StatusNeedsInfo outcome is driven to the same terminal
// states regardless of which attempt produced it. The returned implemented
// bool is explicit ("did the Agent report StatusImplemented", mirroring
// runQualityGates' passed bool) rather than left for callers to re-derive
// by comparing the returned Issue's State.
func (e *Engine) invokeAgent(ctx context.Context, executionID, issueID, workspacePath string, repoCtx agent.RepositoryContext, issue domain.Issue, feedback []agent.Feedback) (_ domain.Issue, implemented bool, _ error) {
	return e.executeAgent(ctx, executionID, issueID, workspacePath, repoCtx, issue, feedback, true)
}

func (e *Engine) continueAgent(ctx context.Context, executionID, issueID, workspacePath string, repoCtx agent.RepositoryContext, issue domain.Issue, feedback []agent.Feedback) (_ domain.Issue, implemented bool, _ error) {
	return e.executeAgent(ctx, executionID, issueID, workspacePath, repoCtx, issue, feedback, false)
}

func (e *Engine) executeAgent(ctx context.Context, executionID, issueID, workspacePath string, repoCtx agent.RepositoryContext, issue domain.Issue, feedback []agent.Feedback, transitionToImplementing bool) (_ domain.Issue, implemented bool, _ error) {
	var err error
	if transitionToImplementing {
		issue, err = e.transition(ctx, executionID, issueID, domain.StateImplementing)
		if err != nil {
			return domain.Issue{}, false, err
		}
	}

	recorder := agent.NewTranscriptRecorder()
	req := agent.AgentRequest{
		WorkspacePath: workspacePath,
		Issue:         issue,
		Repository:    repoCtx,
		Policy:        agent.WorkflowPolicy{},
		Feedback:      feedback,
		Transcript:    recorder,
	}
	contextBytes, err := agent.ContextSizeBytes(req)
	if err != nil {
		return domain.Issue{}, false, fmt.Errorf("engine: encode agent request for issue %s telemetry: %w", issueID, err)
	}
	started := e.Now()
	result, err := e.Agent.Execute(ctx, req)
	finished := e.Now()
	run := storage.AgentRun{
		ExecutionID:  executionID,
		IssueID:      issueID,
		Backend:      e.Config.Agent.Provider,
		StartedAt:    started,
		FinishedAt:   finished,
		Result:       string(result.Status),
		ContextBytes: contextBytes,
	}
	if run.Result == "" && err != nil {
		run.Result = "ERROR"
	}
	if result.Usage != nil {
		inputTokens := result.Usage.InputTokens
		outputTokens := result.Usage.OutputTokens
		run.InputTokens = &inputTokens
		run.OutputTokens = &outputTokens
	}
	agentRunID, recordErr := e.Store.RecordAgentRun(ctx, run)
	if recordErr != nil {
		return domain.Issue{}, false, fmt.Errorf("engine: record agent run for issue %s: %w", issueID, recordErr)
	}
	// Transcript persistence is best-effort per ticket 28: a storage
	// failure here is a durability gap for this attempt's transcript, not
	// a reason to fail the Issue — the AgentRun and its {status, summary}
	// envelope are already durably recorded above regardless.
	_ = e.Store.RecordTranscriptEvents(ctx, executionID, issueID, agentRunID, toStorageTranscriptEvents(recorder.Events()))
	if err != nil {
		return domain.Issue{}, false, fmt.Errorf("engine: agent execute issue %s: %w", issueID, err)
	}
	if err := e.appendEvent(ctx, executionID, issueID, "agent.result", map[string]string{
		"status":  string(result.Status),
		"summary": result.Summary,
	}); err != nil {
		return domain.Issue{}, false, err
	}

	switch result.Status {
	case agent.StatusImplemented:
		return issue, true, nil
	case agent.StatusNeedsInfo:
		issue, err := e.handleNeedsInfo(ctx, executionID, issueID, workerRef(executionID, issueID), result)
		return issue, false, err
	case agent.StatusFailed:
		issue, err := e.transition(ctx, executionID, issueID, domain.StateFailed)
		return issue, false, err
	default:
		return domain.Issue{}, false, fmt.Errorf("engine: agent returned unknown status %q for issue %s", result.Status, issueID)
	}
}

// toStorageTranscriptEvents translates agent.TranscriptEvents (this
// package's Agent-facing capture type) into storage.TranscriptEvents (the
// persisted shape), the same translation convention GateRun/ReviewRun
// document: storage has no dependency on internal/agent, so the engine
// converts between the two.
func toStorageTranscriptEvents(events []agent.TranscriptEvent) []storage.TranscriptEvent {
	out := make([]storage.TranscriptEvent, len(events))
	for i, event := range events {
		out[i] = storage.TranscriptEvent{
			Seq:        event.Seq,
			Type:       string(event.Type),
			Role:       event.Role,
			Text:       event.Text,
			ToolName:   event.ToolName,
			ToolInput:  event.ToolInput,
			ToolOutput: event.ToolOutput,
			OccurredAt: event.Timestamp,
		}
	}
	return out
}

// runQualityGates runs the Issue's configured Quality Gates (ticket 19,
// internal/gate) for an Issue already in VALIDATING, persisting each
// Result via Store.RecordGateRun (the full, bounded stdout/stderr live
// there). All gates passing transitions the Issue to REVIEWING and returns
// every passing Result (so runReview can hand them to the Reviewer as
// gate-results context, per CONTEXT.md "Review"), with a nil failed
// Result. The first failure (Options{} defaults to stop-on-first-fail)
// records a lean "gate.failed" diagnostic Event (name/command/exit_code —
// the Event log stays an audit trail, not a duplicate copy of gate_runs)
// and returns passed=false with the failing Result, leaving the Issue in
// VALIDATING — runRepairLoop's caller (repairAfterGateFailure) decides
// whether that is a retry or a FAILED transition based on the RetryBudget.
//
// The returned passed bool is the caller's explicit "did gates pass"
// signal — callers should branch on it directly rather than re-deriving
// the same fact by comparing the returned Issue's State against
// domain.StateReviewing.
func (e *Engine) runQualityGates(ctx context.Context, executionID, issueID, workspacePath string, issue domain.Issue) (_ domain.Issue, passed bool, _ []gate.Result, failed *gate.Result, _ error) {
	runner := gate.NewRunner(e.Gates)
	results := runner.Run(ctx, workspacePath, e.Config.Quality.Gates, gate.Options{
		MaxOutputBytes: e.Config.Quality.MaxOutputBytes,
	})

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
			return domain.Issue{}, false, nil, nil, fmt.Errorf("engine: record gate run %s for issue %s: %w", res.Name, issueID, err)
		}
		if !res.Passed && failed == nil {
			failed = &results[i]
		}
	}

	if failed == nil {
		issue, err := e.transition(ctx, executionID, issueID, domain.StateReviewing)
		return issue, true, results, nil, err
	}

	// The "gate.failed" Event stays lean (name/command/exit_code, matching
	// "gate.run") rather than duplicating the failing gate's full captured
	// output, which already lives in the persisted GateRun row —
	// repairAfterGateFailure rebuilds agent.Feedback (via gate.BuildFeedback)
	// fresh from that row rather than from this Event.
	if err := e.appendEvent(ctx, executionID, issueID, "gate.failed", map[string]string{
		"name":      failed.Name,
		"command":   failed.Command,
		"exit_code": fmt.Sprint(failed.ExitCode),
	}); err != nil {
		return domain.Issue{}, false, nil, nil, err
	}

	return issue, false, nil, failed, nil
}

// runReview implements REVIEWING once Quality Gates have passed (ticket 20,
// CONTEXT.md "Review"): a fresh review.Reviewer invocation — never the
// implementation Agent's prior conversation — receiving the diff (produced
// by the injected DiffProducer, since Engine stays git-free), the Issue,
// the Repository Context, and gateResults. When Reviewer is unset (the
// optional-seam convention shared with NeedsInfoTracker/Gates — see
// Engine.Reviewer's doc comment), REVIEWING stays a resting state and the
// returned verdict is review.VerdictApproved so runRepairLoop's caller
// (which itself also short-circuits on a nil Reviewer before calling this)
// treats it as done.
//
// review.VerdictApproved transitions the Issue to COMMITTING (ticket 22
// owns the actual commit/PR — this is deliberately another resting state
// for now) and returns a nil Finding slice. review.VerdictChangesRequired
// persists the review run and its Findings but leaves the Issue in
// REVIEWING and returns the Findings verbatim — repairAfterReviewRejection
// (ticket 21's retry loop) is the one that consults the RetryBudget and
// either transitions back to IMPLEMENTING with review.BuildFeedback or
// transitions to FAILED once the review budget is exhausted.
//
// The diff itself only reflects committed work (see cmd/forge's
// gitDiffProducer doc comment): until ticket 22's commit step exists, a
// production Reviewer wired up here would see an empty diff regardless of
// what the Agent changed on disk.
func (e *Engine) runReview(ctx context.Context, executionID, issueID, workerBase, workspacePath string, repoCtx agent.RepositoryContext, issue domain.Issue, gateResults []gate.Result) (domain.Issue, review.Verdict, []review.Finding, error) {
	if e.Reviewer == nil {
		return issue, review.VerdictApproved, nil, nil
	}
	if e.Diff == nil {
		return domain.Issue{}, "", nil, fmt.Errorf("engine: Reviewer is set but Diff (DiffProducer) is nil for issue %s", issueID)
	}

	diff, err := e.Diff.Diff(ctx, workspacePath, workerBase)
	if err != nil {
		return domain.Issue{}, "", nil, fmt.Errorf("engine: produce diff for issue %s: %w", issueID, err)
	}

	started := e.Now()
	result, err := e.Reviewer.Review(ctx, review.Request{
		Diff:        diff,
		Issue:       issue,
		Repository:  repoCtx,
		GateResults: gateResults,
	})
	if err != nil {
		return domain.Issue{}, "", nil, fmt.Errorf("engine: reviewer execute issue %s: %w", issueID, err)
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
		return domain.Issue{}, "", nil, fmt.Errorf("engine: record review run for issue %s: %w", issueID, err)
	}

	switch result.Verdict {
	case review.VerdictApproved:
		issue, err := e.transition(ctx, executionID, issueID, domain.StateCommitting)
		return issue, review.VerdictApproved, nil, err
	case review.VerdictChangesRequired:
		return issue, review.VerdictChangesRequired, result.Findings, nil
	default:
		return domain.Issue{}, "", nil, fmt.Errorf("engine: reviewer returned unknown verdict %q for issue %s", result.Verdict, issueID)
	}
}

// transition moves issueID to state `to` via Store.TransitionIssue, wrapping
// any error (including *domain.InvalidTransitionError, unwrappable via
// errors.As) with the operation's context. It is the single chokepoint
// nearly every engine-driven transition passes through (the two exceptions,
// internal/ci's own DONE/CI_FAILED transitions and forge resume's
// NEEDS_INFO -> READY, reflect status independently or trivially need not —
// see statusreflect.Label), which is why the ticket-24 in-progress/in-review
// signal (internal/statusreflect) is applied here rather than at each
// individual call site: transition reloads the Issue's current state before
// persisting the new one specifically so it has an accurate `from` to hand
// statusreflect.Apply.
func (e *Engine) transition(ctx context.Context, executionID, issueID string, to domain.IssueState) (domain.Issue, error) {
	from, err := e.Store.GetIssue(ctx, executionID, issueID)
	if err != nil {
		return domain.Issue{}, fmt.Errorf("engine: transition issue %s to %s: load current state: %w", issueID, to, err)
	}
	issue, err := e.Store.TransitionIssue(ctx, executionID, issueID, to)
	if err != nil {
		return domain.Issue{}, fmt.Errorf("engine: transition issue %s to %s: %w", issueID, to, err)
	}
	if err := statusreflect.Apply(ctx, e.StatusTracker, e.Config.StatusReflection, issueID, from.State, to); err != nil {
		return domain.Issue{}, fmt.Errorf("engine: transition issue %s to %s: %w", issueID, to, err)
	}
	if err := e.postStatusStartComment(ctx, executionID, issueID, from.State, to); err != nil {
		return domain.Issue{}, fmt.Errorf("engine: transition issue %s to %s: %w", issueID, to, err)
	}
	return issue, nil
}

// postStatusStartComment posts the ticket-24 status-reflection start
// comment (internal/statusreflect) exactly once per (executionID, issueID),
// guarded by a persisted storage.StatusSignalCheckpoint rather than
// statusreflect.Apply's stateless from/to comparison: unlike the label
// swap, AddComment has no tracker-side dedup key, so a retried or resumed
// READY -> CLAIMED transition must consult local state to avoid posting a
// second comment (the same reasoning as handleNeedsInfo's CommentPosted
// checkpoint — see storage.StatusSignalCheckpoint's doc comment).
func (e *Engine) postStatusStartComment(ctx context.Context, executionID, issueID string, from, to domain.IssueState) error {
	if !statusreflect.IsStartTransition(e.Config.StatusReflection, from, to) || e.StatusTracker == nil {
		return nil
	}

	checkpoint, err := e.Store.GetStatusSignalCheckpoint(ctx, executionID, issueID)
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("load status signal checkpoint for issue %s: %w", issueID, err)
	}
	if err == nil && checkpoint.CommentPosted {
		return nil
	}

	if _, err := e.StatusTracker.AddComment(ctx, issueID, statusreflect.StartComment()); err != nil {
		return fmt.Errorf("post status start comment on issue %s: %w", issueID, err)
	}
	return e.Store.SaveStatusSignalCheckpoint(ctx, storage.StatusSignalCheckpoint{
		ExecutionID:   executionID,
		IssueID:       issueID,
		CommentPosted: true,
	})
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

	cancelled := errors.Is(origErr, context.Canceled)
	if !cancelled {
		if err := e.Workspaces.Cleanup(ctx, executionID, issueID); err != nil {
			errs = append(errs, fmt.Errorf("engine: cleanup workspace for issue %s: %w", issueID, err))
		}
	}

	target := domain.StateFailed
	if cancelled {
		target = domain.StateCancelled
	}
	if _, err := e.transition(ctx, executionID, issueID, target); err != nil {
		if target != domain.StateCancelled {
			if _, err := e.transition(ctx, executionID, issueID, domain.StateCancelled); err != nil {
				errs = append(errs, fmt.Errorf("engine: drive issue %s to a terminal state: %w", issueID, err))
			}
		} else {
			errs = append(errs, fmt.Errorf("engine: drive issue %s to a terminal state: %w", issueID, err))
		}
	}
	if cancelled {
		if err := e.Store.ReleaseWorkerClaim(ctx, executionID, issueID); err != nil {
			errs = append(errs, fmt.Errorf("engine: release worker claim for issue %s: %w", issueID, err))
		}
	}

	return errors.Join(errs...)
}
