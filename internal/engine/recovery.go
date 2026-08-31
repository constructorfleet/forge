package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/gate"
	"github.com/Teagan42/forge/internal/repocontext"
	"github.com/Teagan42/forge/internal/review"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
)

// ResumeExecution reconciles one persisted Execution after an orchestrator
// restart, continuing every incomplete Issue from its persisted state
// rather than creating a fresh Execution.
func (e *Engine) ResumeExecution(ctx context.Context, executionID string) (storage.ExecutionState, error) {
	state, err := e.Store.LoadExecution(ctx, executionID)
	if err != nil {
		return storage.ExecutionState{}, fmt.Errorf("engine: resume execution %s: %w", executionID, err)
	}

	for i, issue := range state.Issues {
		resumed, err := e.resumeIssue(ctx, state.Execution, issue)
		if err != nil {
			return storage.ExecutionState{}, fmt.Errorf("engine: resume execution %s issue %s: %w", executionID, issue.ID, err)
		}
		state.Issues[i] = resumed
	}
	return state, nil
}

func (e *Engine) resumeIssue(ctx context.Context, exec domain.Execution, issue domain.Issue) (_ domain.Issue, retErr error) {
	if issue.State.IsTerminal() {
		return issue, nil
	}

	switch issue.State {
	case domain.StateNeedsInfo:
		return e.resumeNeedsInfoIssue(ctx, exec, issue)
	case domain.StateNeedsReplan:
		return e.resumeNeedsReplanIssue(ctx, exec, issue)
	case domain.StateCIPending:
		return e.resumeCIPending(ctx, exec.ID, issue.ID)
	}

	workerBase, err := e.workerBase(ctx, exec, issue.ID)
	if err != nil {
		return domain.Issue{}, err
	}
	if err := e.ensureRecoverableWorker(ctx, exec.ID, issue.ID, issue.State); err != nil {
		return domain.Issue{}, err
	}
	defer func() {
		if err := e.Store.ReleaseWorkerClaim(ctx, exec.ID, issue.ID); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("engine: release worker claim for issue %s: %w", issue.ID, err))
		}
	}()

	switch issue.State {
	case domain.StateReady:
		return e.resumeFromReady(ctx, exec, issue, workerBase)
	case domain.StateClaimed:
		issue, err = e.transition(ctx, exec.ID, issue.ID, domain.StatePreparing)
		if err != nil {
			return domain.Issue{}, err
		}
		return e.resumeFromPreparing(ctx, exec, issue, workerBase)
	case domain.StatePreparing:
		return e.resumeFromPreparing(ctx, exec, issue, workerBase)
	case domain.StateImplementing:
		return e.resumeFromImplementing(ctx, exec, issue, workerBase)
	case domain.StateValidating:
		return e.resumeFromValidating(ctx, exec, issue, workerBase)
	case domain.StateReviewing:
		return e.resumeFromReviewing(ctx, exec, issue, workerBase)
	case domain.StateCommitting:
		return e.resumeFromCommitting(ctx, exec, issue, workerBase)
	case domain.StatePRCreating:
		return e.resumeFromPRCreating(ctx, exec.ID, issue, workerBase)
	default:
		return issue, nil
	}
}

// resumeNeedsReplanIssue is `forge resume`'s handling of an Issue parked by
// a replan escalation (ticket 22): it is resumable only once a fresh plan
// has been approved and the Feature unfrozen, which is exactly what
// ResumeAfterReplan enforces. While the Feature is still frozen the Issue is
// returned untouched — quiesced work takes no Worker claim and makes no
// progress — and once it is not, the result is revalidated on its way back
// to READY and resumption continues from there.
func (e *Engine) resumeNeedsReplanIssue(ctx context.Context, exec domain.Execution, issue domain.Issue) (domain.Issue, error) {
	resumed, err := e.ResumeAfterReplan(ctx, exec.ID, issue.ID)
	if err != nil {
		var frozen *FeatureFrozenError
		if errors.As(err, &frozen) {
			return issue, nil
		}
		return domain.Issue{}, err
	}
	return e.resumeIssue(ctx, exec, resumed)
}

func (e *Engine) resumeNeedsInfoIssue(ctx context.Context, exec domain.Execution, issue domain.Issue) (domain.Issue, error) {
	resumeTracker, ok := e.NeedsInfoTracker.(ResumeTracker)
	if !ok {
		return issue, nil
	}
	result, err := Resume(ctx, e.Store, resumeTracker, exec.ID, issue.ID, e.Now)
	if err != nil {
		return domain.Issue{}, err
	}
	if !result.Resumed {
		return result.Issue, nil
	}
	return e.resumeIssue(ctx, exec, result.Issue)
}

func (e *Engine) resumeFromReady(ctx context.Context, exec domain.Execution, issue domain.Issue, workerBase string) (domain.Issue, error) {
	if err := e.Store.ClaimIssue(ctx, exec.ID, issue.ID, workerRef(exec.ID, issue.ID)); err != nil && !errors.Is(err, storage.ErrAlreadyClaimed) {
		return domain.Issue{}, fmt.Errorf("engine: claim issue %s: %w", issue.ID, err)
	}
	if err := e.Store.UpdateWorkerOwner(ctx, exec.ID, issue.ID, e.OwnerPID()); err != nil {
		return domain.Issue{}, fmt.Errorf("engine: record worker owner for issue %s: %w", issue.ID, err)
	}
	issue, err := e.transition(ctx, exec.ID, issue.ID, domain.StateClaimed)
	if err != nil {
		return domain.Issue{}, err
	}
	issue, err = e.transition(ctx, exec.ID, issue.ID, domain.StatePreparing)
	if err != nil {
		return domain.Issue{}, err
	}
	return e.resumeFromPreparing(ctx, exec, issue, workerBase)
}

func (e *Engine) resumeFromPreparing(ctx context.Context, exec domain.Execution, issue domain.Issue, workerBase string) (domain.Issue, error) {
	ws, err := e.ensureWorkspace(ctx, exec.ID, issue.ID, workerBase)
	if err != nil {
		return domain.Issue{}, err
	}
	repoCtx, err := repocontext.Compile(e.Config, e.RepoRoot, workerBase)
	if err != nil {
		return domain.Issue{}, fmt.Errorf("engine: compile repository context: %w", err)
	}
	issue, implemented, err := e.invokeAgent(ctx, exec.ID, issue.ID, ws.Path, repoCtx, issue, nil)
	if err != nil {
		return domain.Issue{}, err
	}
	if !implemented {
		return issue, nil
	}
	issue, err = e.runRepairLoop(ctx, exec.ID, issue.ID, workerBase, ws.Path, repoCtx, issue)
	if err != nil {
		return domain.Issue{}, err
	}
	if issue.State == domain.StateCommitting {
		issue, err = e.runCommitAndPR(ctx, exec.ID, issue.ID, workerBase, ws, issue)
		if err != nil {
			return domain.Issue{}, err
		}
	}
	if issue.State == domain.StateCIPending && e.CIWaiter != nil {
		return e.resumeCIPending(ctx, exec.ID, issue.ID)
	}
	return issue, nil
}

func (e *Engine) resumeFromImplementing(ctx context.Context, exec domain.Execution, issue domain.Issue, workerBase string) (domain.Issue, error) {
	ws, err := e.ensureWorkspace(ctx, exec.ID, issue.ID, workerBase)
	if err != nil {
		return domain.Issue{}, err
	}
	repoCtx, err := repocontext.Compile(e.Config, e.RepoRoot, workerBase)
	if err != nil {
		return domain.Issue{}, fmt.Errorf("engine: compile repository context: %w", err)
	}
	issue, implemented, err := e.continueAgent(ctx, exec.ID, issue.ID, ws.Path, repoCtx, issue, nil)
	if err != nil {
		return domain.Issue{}, err
	}
	if !implemented {
		return issue, nil
	}
	issue, err = e.runRepairLoop(ctx, exec.ID, issue.ID, workerBase, ws.Path, repoCtx, issue)
	if err != nil {
		return domain.Issue{}, err
	}
	if issue.State == domain.StateCommitting {
		issue, err = e.runCommitAndPR(ctx, exec.ID, issue.ID, workerBase, ws, issue)
		if err != nil {
			return domain.Issue{}, err
		}
	}
	if issue.State == domain.StateCIPending && e.CIWaiter != nil {
		return e.resumeCIPending(ctx, exec.ID, issue.ID)
	}
	return issue, nil
}

func (e *Engine) resumeFromValidating(ctx context.Context, exec domain.Execution, issue domain.Issue, workerBase string) (domain.Issue, error) {
	ws, err := e.ensureWorkspace(ctx, exec.ID, issue.ID, workerBase)
	if err != nil {
		return domain.Issue{}, err
	}
	repoCtx, err := repocontext.Compile(e.Config, e.RepoRoot, workerBase)
	if err != nil {
		return domain.Issue{}, fmt.Errorf("engine: compile repository context: %w", err)
	}
	issue, passed, gateResults, failedGate, err := e.runQualityGates(ctx, exec.ID, issue.ID, ws.Path, issue)
	if err != nil {
		return domain.Issue{}, err
	}
	if !passed {
		issue, err := e.resumeAfterFailedGate(ctx, exec.ID, issue.ID, ws.Path, repoCtx, issue, failedGate)
		if err != nil {
			return domain.Issue{}, err
		}
		if issue.State == domain.StateImplementing {
			return e.resumeFromImplementing(ctx, exec, issue, workerBase)
		}
		return issue, nil
	}
	issue, verdict, findings, err := e.runReview(ctx, exec.ID, issue.ID, workerBase, ws.Path, repoCtx, issue, gateResults)
	if err != nil {
		return domain.Issue{}, err
	}
	if verdict == review.VerdictChangesRequired {
		issue, retried, err := e.repair(ctx, exec.ID, issue.ID, ws.Path, repoCtx, issue,
			issue.RetryBudget.ReviewExhausted(),
			func() (domain.Issue, error) {
				return e.escalateReviewToNeedsInfo(ctx, exec.ID, issue.ID,
					"Review requested changes and the review retry budget is exhausted; human input is needed to proceed.",
					reviewFindingsContext(findings))
			},
			(*domain.Issue).RecordReviewRejection, review.BuildFeedback(findings), "review")
		if err != nil || !retried {
			return issue, err
		}
		return e.resumeFromImplementing(ctx, exec, issue, workerBase)
	}
	if issue.State == domain.StateCommitting {
		issue, err = e.runCommitAndPR(ctx, exec.ID, issue.ID, workerBase, ws, issue)
		if err != nil {
			return domain.Issue{}, err
		}
	}
	if issue.State == domain.StateCIPending && e.CIWaiter != nil {
		return e.resumeCIPending(ctx, exec.ID, issue.ID)
	}
	return issue, nil
}

func (e *Engine) resumeAfterFailedGate(ctx context.Context, executionID, issueID, workspacePath string, repoCtx agent.RepositoryContext, issue domain.Issue, failedGate *gate.Result) (domain.Issue, error) {
	if failedGate == nil {
		return issue, nil
	}
	issue, retried, err := e.repair(ctx, executionID, issueID, workspacePath, repoCtx, issue,
		issue.RetryBudget.GateExhausted(),
		func() (domain.Issue, error) {
			return e.transition(ctx, executionID, issueID, domain.StateFailed)
		},
		(*domain.Issue).RecordGateFailure, []agent.Feedback{gate.BuildFeedback(*failedGate)}, "gate")
	if err != nil || !retried {
		return issue, err
	}
	return issue, nil
}

func (e *Engine) resumeFromReviewing(ctx context.Context, exec domain.Execution, issue domain.Issue, workerBase string) (domain.Issue, error) {
	if e.Reviewer == nil {
		return issue, nil
	}
	ws, err := e.ensureWorkspace(ctx, exec.ID, issue.ID, workerBase)
	if err != nil {
		return domain.Issue{}, err
	}
	repoCtx, err := repocontext.Compile(e.Config, e.RepoRoot, workerBase)
	if err != nil {
		return domain.Issue{}, fmt.Errorf("engine: compile repository context: %w", err)
	}
	issue, verdict, findings, err := e.runReview(ctx, exec.ID, issue.ID, workerBase, ws.Path, repoCtx, issue, nil)
	if err != nil {
		return domain.Issue{}, err
	}
	if verdict == review.VerdictChangesRequired {
		issue, retried, err := e.repair(ctx, exec.ID, issue.ID, ws.Path, repoCtx, issue,
			issue.RetryBudget.ReviewExhausted(),
			func() (domain.Issue, error) {
				return e.escalateReviewToNeedsInfo(ctx, exec.ID, issue.ID,
					"Review requested changes and the review retry budget is exhausted; human input is needed to proceed.",
					reviewFindingsContext(findings))
			},
			(*domain.Issue).RecordReviewRejection, review.BuildFeedback(findings), "review")
		if err != nil || !retried {
			return issue, err
		}
		return e.resumeFromImplementing(ctx, exec, issue, workerBase)
	}
	return e.resumeFromCommitting(ctx, exec, issue, workerBase)
}

func (e *Engine) resumeFromCommitting(ctx context.Context, exec domain.Execution, issue domain.Issue, workerBase string) (domain.Issue, error) {
	ws, err := e.ensureWorkspace(ctx, exec.ID, issue.ID, workerBase)
	if err != nil {
		return domain.Issue{}, err
	}
	issue, err = e.runCommitAndPR(ctx, exec.ID, issue.ID, workerBase, ws, issue)
	if err != nil {
		return domain.Issue{}, err
	}
	if issue.State == domain.StateCIPending && e.CIWaiter != nil {
		return e.resumeCIPending(ctx, exec.ID, issue.ID)
	}
	return issue, nil
}

func (e *Engine) resumeFromPRCreating(ctx context.Context, executionID string, issue domain.Issue, workerBase string) (domain.Issue, error) {
	ws, err := e.ensureWorkspace(ctx, executionID, issue.ID, workerBase)
	if err != nil {
		return domain.Issue{}, err
	}
	if e.Publisher == nil || e.PRTracker == nil {
		return issue, nil
	}
	summary, err := e.agentSummary(ctx, executionID, issue.ID)
	if err != nil {
		return domain.Issue{}, err
	}
	sha, err := e.Publisher.Commit(ctx, ws.Path, e.commitMessage(issue, summary))
	if err != nil {
		return domain.Issue{}, fmt.Errorf("engine: commit issue %s: %w", issue.ID, err)
	}
	pr, err := e.PRTracker.CreatePullRequest(ctx, tracker.PullRequestRequest{
		Base:  e.BaseBranch,
		Head:  ws.Branch,
		Title: prTitle(issue),
		Body:  prBody(issue, summary),
	})
	if err != nil {
		return domain.Issue{}, fmt.Errorf("engine: create pull request for issue %s: %w", issue.ID, err)
	}
	if err := e.Store.RecordPullRequest(ctx, storage.PullRequest{
		ExecutionID: executionID,
		IssueID:     issue.ID,
		Number:      pr.Number,
		URL:         pr.URL,
		CommitSHA:   sha,
		CreatedAt:   e.Now(),
	}); err != nil {
		return domain.Issue{}, fmt.Errorf("engine: persist pull request for issue %s: %w", issue.ID, err)
	}
	issue, err = e.transition(ctx, executionID, issue.ID, domain.StateCIPending)
	if err != nil {
		return domain.Issue{}, err
	}
	if e.CIWaiter != nil {
		return e.resumeCIPending(ctx, executionID, issue.ID)
	}
	return issue, nil
}

func (e *Engine) resumeCIPending(ctx context.Context, executionID, issueID string) (domain.Issue, error) {
	if e.CIWaiter == nil {
		return e.Store.GetIssue(ctx, executionID, issueID)
	}
	if _, err := e.CIWaiter.Wait(ctx, executionID, issueID); err != nil {
		return domain.Issue{}, err
	}
	return e.Store.GetIssue(ctx, executionID, issueID)
}

func (e *Engine) workerBase(ctx context.Context, exec domain.Execution, issueID string) (string, error) {
	events, err := e.Store.EventsByIssue(ctx, exec.ID, issueID)
	if err != nil {
		return "", fmt.Errorf("engine: load events for issue %s: %w", issueID, err)
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != "worker.base_captured" {
			continue
		}
		var payload struct {
			Base string `json:"base"`
		}
		if err := json.Unmarshal([]byte(events[i].Data), &payload); err != nil {
			return "", fmt.Errorf("engine: decode worker base for issue %s: %w", issueID, err)
		}
		if payload.Base != "" {
			return payload.Base, nil
		}
	}
	if exec.BaseRevision == "" {
		return "", fmt.Errorf("engine: issue %s has no captured worker base", issueID)
	}
	return exec.BaseRevision, nil
}

func (e *Engine) ensureRecoverableWorker(ctx context.Context, executionID, issueID string, state domain.IssueState) error {
	if state == domain.StateReady || state == domain.StateNeedsInfo || state == domain.StateCIPending {
		return nil
	}
	claim, err := e.Store.WorkerClaim(ctx, executionID, issueID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			if err := e.Store.ClaimIssue(ctx, executionID, issueID, workerRef(executionID, issueID)); err != nil && !errors.Is(err, storage.ErrAlreadyClaimed) {
				return err
			}
			return e.Store.UpdateWorkerOwner(ctx, executionID, issueID, e.OwnerPID())
		}
		return err
	}
	running, err := e.ProcessRunning(claim.OwnerPID)
	if err != nil {
		return fmt.Errorf("engine: inspect worker owner for issue %s: %w", issueID, err)
	}
	if running && claim.OwnerPID != e.OwnerPID() {
		return fmt.Errorf("engine: issue %s is still owned by live process %d", issueID, claim.OwnerPID)
	}
	if !running {
		if err := e.appendEvent(ctx, executionID, issueID, "worker.recovered", map[string]string{
			"worker_ref": claim.WorkerRef,
			"owner_pid":  fmt.Sprint(claim.OwnerPID),
		}); err != nil {
			return err
		}
	}
	return e.Store.UpdateWorkerOwner(ctx, executionID, issueID, e.OwnerPID())
}

func (e *Engine) ensureWorkspace(ctx context.Context, executionID, issueID, workerBase string) (domain.Workspace, error) {
	ws, err := e.Workspaces.Validate(ctx, executionID, issueID)
	if err == nil {
		if recErr := e.Store.RecordWorkspace(ctx, executionID, ws); recErr != nil {
			return domain.Workspace{}, fmt.Errorf("engine: persist workspace for issue %s: %w", issueID, recErr)
		}
		return ws, nil
	}
	if cleanupErr := e.Workspaces.Cleanup(ctx, executionID, issueID); cleanupErr != nil {
		return domain.Workspace{}, fmt.Errorf("engine: cleanup unhealthy workspace for issue %s: %w", issueID, cleanupErr)
	}

	ws, createErr := e.Workspaces.Create(ctx, executionID, issueID, workerBase)
	if createErr != nil {
		return domain.Workspace{}, fmt.Errorf("engine: create workspace for issue %s: %w", issueID, createErr)
	}
	if recErr := e.Store.RecordWorkspace(ctx, executionID, ws); recErr != nil {
		return domain.Workspace{}, fmt.Errorf("engine: persist workspace for issue %s: %w", issueID, recErr)
	}
	if appErr := e.appendEvent(ctx, executionID, issueID, "workspace.recovered", map[string]string{
		"path":   ws.Path,
		"branch": ws.Branch,
	}); appErr != nil {
		return domain.Workspace{}, appErr
	}
	return ws, nil
}
