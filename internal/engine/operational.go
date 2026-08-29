package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
)

// RebaseConflictError is returned by RetryIssue when refreshing a retried
// Issue's Worker base (ticket 29) hits a rebase conflict onto the target
// branch's new tip. The Issue is left FAILED (refreshRetryBase runs before
// RetryIssue mutates any other retry state) and this error names the
// offending tip and conflicting paths, so a human or agent knows exactly
// what to resolve instead of seeing a generic git error.
type RebaseConflictError struct {
	IssueID string
	Base    string
	Paths   []string
}

func (e *RebaseConflictError) Error() string {
	return fmt.Sprintf("FAILED: rebase conflict onto %s: %s", e.Base, strings.Join(e.Paths, ", "))
}

// CancelExecution interrupts any live worker owners for executionID, then
// marks every non-terminal Issue in the Execution CANCELLED and releases
// its worker claim.
func (e *Engine) CancelExecution(ctx context.Context, executionID string) (storage.ExecutionState, error) {
	state, err := e.Store.LoadExecution(ctx, executionID)
	if err != nil {
		return storage.ExecutionState{}, fmt.Errorf("engine: cancel execution %s: %w", executionID, err)
	}

	if err := e.appendEvent(ctx, executionID, "", "execution.cancel_requested", map[string]string{}); err != nil {
		return storage.ExecutionState{}, err
	}

	for _, pid := range workerOwners(ctx, e.Store, executionID, state.Issues) {
		if pid == e.OwnerPID() {
			continue
		}
		if err := e.InterruptProcess(pid); err != nil {
			return storage.ExecutionState{}, fmt.Errorf("engine: interrupt worker owner %d: %w", pid, err)
		}
		if err := e.WaitForProcessExit(ctx, pid); err != nil {
			return storage.ExecutionState{}, fmt.Errorf("engine: wait for worker owner %d: %w", pid, err)
		}
	}

	for _, issue := range state.Issues {
		if issue.State.IsTerminal() {
			continue
		}
		if issue.State != domain.StateCancelled {
			if _, err := e.transition(ctx, executionID, issue.ID, domain.StateCancelled); err != nil {
				reloaded, getErr := e.Store.GetIssue(ctx, executionID, issue.ID)
				if getErr != nil {
					return storage.ExecutionState{}, fmt.Errorf("engine: cancel issue %s: %w", issue.ID, err)
				}
				if reloaded.State != domain.StateCancelled {
					return storage.ExecutionState{}, fmt.Errorf("engine: cancel issue %s: %w", issue.ID, err)
				}
			}
		}
		if err := e.Store.ReleaseWorkerClaim(ctx, executionID, issue.ID); err != nil {
			return storage.ExecutionState{}, fmt.Errorf("engine: release worker claim for issue %s: %w", issue.ID, err)
		}
	}

	if err := e.appendEvent(ctx, executionID, "", "execution.cancelled", map[string]string{}); err != nil {
		return storage.ExecutionState{}, err
	}
	return e.Store.LoadExecution(ctx, executionID)
}

// RetryIssue reruns a FAILED Issue within its existing Execution, reusing
// the recorded Worker base and recovering the existing Workspace when
// possible.
func (e *Engine) RetryIssue(ctx context.Context, executionID, issueID string) (domain.Issue, error) {
	state, err := e.Store.LoadExecution(ctx, executionID)
	if err != nil {
		return domain.Issue{}, fmt.Errorf("engine: retry issue %s: load execution %s: %w", issueID, executionID, err)
	}
	issue, err := e.Store.GetIssue(ctx, executionID, issueID)
	if err != nil {
		return domain.Issue{}, fmt.Errorf("engine: retry issue %s: %w", issueID, err)
	}
	if issue.State != domain.StateFailed {
		return domain.Issue{}, fmt.Errorf("engine: issue %s is %s, want FAILED", issueID, issue.State)
	}
	if err := e.refreshRetryBase(ctx, state.Execution, issueID); err != nil {
		return domain.Issue{}, err
	}
	issue.RetryBudget = domain.NewRetryBudget(issue.RetryBudget.Limits())
	if err := e.Store.UpdateRetryBudget(ctx, executionID, issueID, issue.RetryBudget); err != nil {
		return domain.Issue{}, fmt.Errorf("engine: reset retry budget for issue %s: %w", issueID, err)
	}
	if err := e.Store.ReleaseWorkerClaim(ctx, executionID, issueID); err != nil {
		return domain.Issue{}, fmt.Errorf("engine: release worker claim for issue %s: %w", issueID, err)
	}
	issue, err = e.transition(ctx, executionID, issueID, domain.StateReady)
	if err != nil {
		return domain.Issue{}, err
	}
	if err := e.appendEvent(ctx, executionID, issueID, "issue.retry_requested", map[string]string{}); err != nil {
		return domain.Issue{}, err
	}
	return e.resumeIssue(ctx, state.Execution, issue)
}

// refreshRetryBase re-resolves executionID/issueID's Worker base to the
// target branch's current tip before RetryIssue re-runs it (ticket 29): ADR
// 0006 captures a Worker's base once, at its original READY transition, so
// without this a retry keeps branching from a base that predates whatever
// unrelated commits have since merged into the target branch, inviting
// merge-conflicted, behind-target PRs that have nothing to do with the
// Issue.
//
// The refresh only ever moves forward: newBase must still contain the
// previously captured base (verified via Ancestry when wired), so every
// already-merged Dependency the original base captured (ADR 0005/0006)
// remains present in the refreshed one — a refresh that would drop one is
// refused, not silently applied. If TargetTip is unset, retry keeps ADR
// 0006's originally captured base, matching pre-ticket-29 behavior.
//
// It runs before RetryIssue resets the retry budget, releases the worker
// claim, or transitions the Issue to READY, so a refusal or rebase conflict
// leaves the FAILED Issue exactly as it was — safe to retry again once the
// underlying problem (e.g. a genuinely diverged base, or an unresolved
// conflict) is addressed.
//
// This is scoped to retry of a terminal FAILED Issue only. resumeIssue's
// other, in-flight resume paths (used by ResumeExecution after an
// orchestrator restart) intentionally keep ADR 0006's originally captured
// base: a Worker still mid-flight is not "a fresh attempt" the way a
// retried FAILED Issue already implies.
func (e *Engine) refreshRetryBase(ctx context.Context, exec domain.Execution, issueID string) error {
	if e.TargetTip == nil {
		return nil
	}

	oldBase, err := e.workerBase(ctx, exec, issueID)
	if err != nil {
		return err
	}
	newBase, err := e.TargetTip.CurrentTip(ctx)
	if err != nil {
		return fmt.Errorf("engine: resolve target tip for issue %s retry: %w", issueID, err)
	}
	if newBase == "" || newBase == oldBase {
		return nil
	}

	if e.Ancestry != nil {
		ok, err := e.Ancestry.IsAncestor(ctx, oldBase, newBase)
		if err != nil {
			return fmt.Errorf("engine: verify base refresh for issue %s (%s -> %s): %w", issueID, oldBase, newBase, err)
		}
		if !ok {
			return fmt.Errorf(
				"engine: refusing to refresh issue %s base %s -> %s: %s does not descend from the current base, which would risk dropping a merged dependency",
				issueID, oldBase, newBase, newBase)
		}
	}

	if _, err := e.Workspaces.Validate(ctx, exec.ID, issueID); err == nil {
		rebaser, ok := e.Workspaces.(WorkspaceRebaser)
		if !ok {
			return fmt.Errorf(
				"engine: cannot refresh base for issue %s: a Workspace exists but Workspaces does not support Rebase", issueID)
		}
		conflicts, err := rebaser.Rebase(ctx, exec.ID, issueID, newBase)
		if err != nil {
			return fmt.Errorf("engine: rebase issue %s workspace onto %s: %w", issueID, newBase, err)
		}
		if len(conflicts) > 0 {
			if err := e.appendEvent(ctx, exec.ID, issueID, "worker.base_refresh_conflict", map[string]string{
				"old_base": oldBase,
				"new_base": newBase,
				"paths":    strings.Join(conflicts, ","),
			}); err != nil {
				return err
			}
			return &RebaseConflictError{IssueID: issueID, Base: newBase, Paths: conflicts}
		}
	}

	return e.appendEvent(ctx, exec.ID, issueID, "worker.base_captured", map[string]string{
		"base":     newBase,
		"old_base": oldBase,
	})
}

func workerOwners(ctx context.Context, store storage.Store, executionID string, issues []domain.Issue) []int {
	owners := map[int]struct{}{}
	for _, issue := range issues {
		claim, err := store.WorkerClaim(ctx, executionID, issue.ID)
		if err != nil || claim.OwnerPID <= 0 {
			continue
		}
		owners[claim.OwnerPID] = struct{}{}
	}
	pids := make([]int, 0, len(owners))
	for pid := range owners {
		pids = append(pids, pid)
	}
	sort.Ints(pids)
	return pids
}
