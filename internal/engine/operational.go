package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
)

// ErrRetryAlreadyClaimed reports that another actor already claimed the
// retry (issue 456). Store.ClaimRetry elects one winner; the loser gets this
// instead of the raw storage.ErrConcurrentModification, because for the
// operator it is a no-op, not a failure.
var ErrRetryAlreadyClaimed = errors.New("engine: another actor already claimed this retry")

// RebaseConflictError is returned by RetryIssue when refreshing a retried
// Issue's Worker base (ticket 29) hits a rebase conflict onto the target
// branch's new tip. The Issue is left FAILED (RetryIssue rolls its retry
// claim back with Store.AbortRetry) and this error names the
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
//
// Store.ClaimRetry applies every retry mutation as one compare-and-set
// transaction, so two actors that both pass the FAILED check elect exactly
// one winner (issue 456). The loser gets ErrRetryAlreadyClaimed and changes
// nothing: separate statements let it reset the budget under the winner and
// release the winner's fresh Worker claim, and let it rebase the Workspace
// the winner is already working in.
//
// The claim therefore comes first, before the base refresh, and a refresh
// that fails is rolled back with Store.AbortRetry — a rebase conflict must
// leave the Issue FAILED, which is what the operator then acts on. The
// rollback covers the base refresh only. A failure after that leaves the
// Issue READY with a reset budget, which the scheduler picks up as a normal
// retry.
//
// Claiming first also publishes the Issue as READY with no Worker claim for
// the duration of the base refresh, and a concurrent `forge resume` reads
// that shape as resumable. The overlap is safe but not free: both actors
// then work in one Workspace. Take the repo lock here if a second
// long-lived actor (a TUI) makes the window routine.
func (e *Engine) RetryIssue(ctx context.Context, executionID, issueID string) (domain.Issue, error) {
	state, err := e.Store.LoadExecution(ctx, executionID)
	if err != nil {
		return domain.Issue{}, fmt.Errorf("engine: retry issue %s: load execution %s: %w", issueID, executionID, err)
	}
	// Read only for the budget: its limits seed the reset budget and its
	// counters are the rollback value. ClaimRetry owns the FAILED check.
	issue, err := e.Store.GetIssue(ctx, executionID, issueID)
	if err != nil {
		return domain.Issue{}, fmt.Errorf("engine: retry issue %s: %w", issueID, err)
	}

	failedBudget := issue.RetryBudget
	claim, err := e.Store.ClaimRetry(ctx, executionID, issueID, domain.NewRetryBudget(failedBudget.Limits()))
	if err != nil {
		return domain.Issue{}, retryClaimError(issueID, issue.State, err)
	}
	if err := e.refreshRetryBase(ctx, state.Execution, issueID); err != nil {
		if abortErr := e.Store.AbortRetry(ctx, executionID, issueID, failedBudget); abortErr != nil {
			return domain.Issue{}, errors.Join(err, abortErr)
		}
		return domain.Issue{}, err
	}
	if err := e.applyTransitionEffects(ctx, executionID, issueID, claim.From, claim.Issue.State); err != nil {
		return domain.Issue{}, err
	}
	if err := e.appendEvent(ctx, executionID, issueID, "issue.retry_requested", map[string]string{}); err != nil {
		return domain.Issue{}, err
	}
	return e.resumeIssue(ctx, state.Execution, claim.Issue)
}

// retryClaimError maps a lost retry claim to the reason the operator needs.
// The conflict state alone cannot tell a rival retry from an Issue that was
// never FAILED: a rival winner moves straight on through READY, CLAIMED and
// later states, and a queued Issue that never failed also sits in READY. The
// pre-claim state `observed` makes the distinction. Only a claim that read
// FAILED and then lost the compare-and-set is a race, and a race a cancel
// won leaves CANCELLED, which is a real failure.
func retryClaimError(issueID string, observed domain.IssueState, err error) error {
	var conflict *storage.RetryClaimConflictError
	if !errors.As(err, &conflict) {
		return fmt.Errorf("engine: claim retry for issue %s: %w", issueID, err)
	}
	if observed != domain.StateFailed {
		return fmt.Errorf("engine: retry issue %s: issue is %s, want FAILED", issueID, observed)
	}
	if conflict.State == domain.StateCancelled {
		return fmt.Errorf("engine: retry issue %s: issue is %s, want FAILED", issueID, conflict.State)
	}
	return fmt.Errorf("engine: retry issue %s: %w", issueID, ErrRetryAlreadyClaimed)
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
// It runs after Store.ClaimRetry, which has already reset the retry budget,
// released the Worker claim, and transitioned the Issue to READY. A refusal
// or rebase conflict therefore returns an error, and RetryIssue undoes the
// claim with Store.AbortRetry: the Issue goes back to FAILED with its
// pre-retry budget — safe to retry again once the underlying problem (e.g. a
// genuinely diverged base, or an unresolved conflict) is addressed. The
// rollback does not put back the released Worker claim; see
// storage.Store.AbortRetry for why no reader needs it.
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
