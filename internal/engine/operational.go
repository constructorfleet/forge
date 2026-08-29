package engine

import (
	"context"
	"fmt"
	"sort"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
)

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
