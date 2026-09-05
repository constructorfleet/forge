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

// RetryStartDeferredError reports that RetryIssue's claim committed, but a
// step after the claim (reflecting the transition to the tracker, recording
// the issue.retry_requested event, or an early failure inside resumeIssue's
// own re-entry) then failed while the Issue was still READY. Unlike a failed
// claim, this is not undone: the Issue keeps its claimed, reset retry budget
// and released Worker claim, the shape the scheduler already treats as ready
// to run. Callers must report this as a deferred start, not a failed retry:
// the retry itself succeeded, and the scheduler picks the Issue up on its
// own.
type RetryStartDeferredError struct {
	Err error
}

func (e *RetryStartDeferredError) Error() string {
	return fmt.Sprintf("retry claimed; start deferred to the scheduler: %v", e.Err)
}

func (e *RetryStartDeferredError) Unwrap() error { return e.Err }

// RetryResumeStuckError reports that RetryIssue's claim committed and
// resumeIssue then advanced the Issue past READY (for example to CLAIMED or
// PREPARING, by claiming the Issue and transitioning it forward) before it
// failed. Unlike RetryStartDeferredError, the scheduler does not pick this
// up on its own: the Issue is left mid-resume with no Worker claim, and
// continuing it needs an explicit operator-run `forge resume
// <execution-id>`. Callers must report this as a failed retry that needs
// operator action, not a deferred start.
type RetryResumeStuckError struct {
	Err   error
	State domain.IssueState
}

func (e *RetryResumeStuckError) Error() string {
	return fmt.Sprintf("retry claimed but stuck mid-resume at %s; run forge resume: %v", e.State, e.Err)
}

func (e *RetryResumeStuckError) Unwrap() error { return e.Err }

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

// CancelOwnerError reports that CancelExecution could not stop, or could not
// inspect, one or more worker owners. The cancel itself is complete: every
// non-terminal Issue is CANCELLED, and every Issue whose owner did not stop
// or could not be inspected keeps its Worker claim. Callers must test for
// this error and report it as a warning, not as a failed cancel.
type CancelOwnerError struct {
	Err error
}

func (e *CancelOwnerError) Error() string {
	return fmt.Sprintf("worker owner not stopped or not inspected: %v", e.Err)
}

func (e *CancelOwnerError) Unwrap() error { return e.Err }

// CancelExecution interrupts any live worker owners for executionID, then
// marks every non-terminal Issue in the Execution CANCELLED and releases
// its worker claim. It keeps the claim of an owner that did not stop, and of
// an owner it could not inspect, and it names that Issue and pid in the
// execution.cancelled event.
//
// A non-nil error of type *CancelOwnerError reports only that a worker owner
// did not stop or could not be inspected; the Issues are cancelled and the
// returned ExecutionState is valid. Callers must not read such an error as
// "nothing was cancelled". Any other non-nil error comes with a zero
// ExecutionState, even when earlier Issues in the loop already transitioned.
// withIssueLock runs fn with executionID/issueID's IssueLock held, so
// CancelExecution's per-Issue cancel (itself one CAS transaction; see
// Store.ClaimCancel, issue 554) and RetryIssue's claim-through-resume
// cannot interleave for the same Issue (issue 552).
func (e *Engine) withIssueLock(ctx context.Context, executionID, issueID string, fn func() error) error {
	return e.IssueLock.WithLock(ctx, fmt.Sprintf("issue:%s/%s", executionID, issueID), fn)
}

func (e *Engine) CancelExecution(ctx context.Context, executionID string) (storage.ExecutionState, error) {
	state, err := e.Store.LoadExecution(ctx, executionID)
	if err != nil {
		return storage.ExecutionState{}, fmt.Errorf("engine: cancel execution %s: %w", executionID, err)
	}

	if err := e.appendEvent(ctx, executionID, "", "execution.cancel_requested", map[string]string{}); err != nil {
		return storage.ExecutionState{}, err
	}

	// An owner that does not stop is a problem to report, but it must not
	// stop the CANCELLED transitions below. A cancel that signals a worker
	// and then leaves every Issue in its previous state is worse than one
	// that cancels the Issues and reports the unresponsive owner.
	owners, ownerErr := e.workerOwnersOf(ctx, executionID, state.Issues)
	stopped := map[int]struct{}{}
	for _, pid := range sortedOwnerPIDs(owners.live) {
		if pid == e.OwnerPID() {
			stopped[pid] = struct{}{}
			continue
		}
		if err := e.InterruptProcess(pid); err != nil {
			ownerErr = errors.Join(ownerErr, fmt.Errorf("engine: interrupt worker owner %d: %w", pid, err))
			continue
		}
		if err := e.WaitForProcessExit(ctx, pid); err != nil {
			ownerErr = errors.Join(ownerErr, fmt.Errorf("engine: wait for worker owner %d: %w", pid, err))
			continue
		}
		stopped[pid] = struct{}{}
	}

	var kept []string
	for _, issue := range state.Issues {
		if issue.State.IsTerminal() {
			continue
		}
		// The per-Issue mutation runs under IssueLock, which a concurrent
		// RetryIssue holds for its whole claim-through-resume span (issue
		// 552). By the time this side acquires it, that retry has either
		// finished (the Issue may since have gone terminal, e.g. DONE) or
		// never started, so the state is re-read fresh rather than trusting
		// the state.Issues snapshot taken before the wait.
		var keptEntry string
		lockErr := e.withIssueLock(ctx, executionID, issue.ID, func() error {
			current, err := e.Store.GetIssue(ctx, executionID, issue.ID)
			if err != nil {
				return fmt.Errorf("engine: cancel issue %s: %w", issue.ID, err)
			}
			if current.State.IsTerminal() {
				return nil
			}
			// workers.issue_id is globally unique, so releasing the claim
			// of an owner that still runs would let a second Execution
			// claim the same Issue while the first owner still writes to
			// it. Keep the claim.
			pid, keep := owners.keeps(issue.ID, stopped)

			// ClaimCancel applies the CANCELLED transition and the claim
			// release (or keep) as one transaction, CASed off current.State
			// (issue 554): anything that moved the Issue off current.State
			// between the read above and this call loses this write
			// instead of silently interleaving with it.
			if _, err := e.Store.ClaimCancel(ctx, executionID, issue.ID, current.State, !keep); err != nil {
				var conflict *storage.CancelClaimConflictError
				if errors.As(err, &conflict) {
					if conflict.State.IsTerminal() {
						// Another actor (e.g. a concurrent duplicate cancel)
						// already finished this Issue; nothing left to do.
						return nil
					}
					return fmt.Errorf("engine: cancel issue %s: another actor moved it to %s: %w", issue.ID, conflict.State, err)
				}
				return fmt.Errorf("engine: cancel issue %s: %w", issue.ID, err)
			}
			if err := e.applyTransitionEffects(ctx, executionID, issue.ID, current.State, domain.StateCancelled); err != nil {
				return err
			}
			if keep {
				keptEntry = fmt.Sprintf("%s:%d", issue.ID, pid)
			}
			return nil
		})
		if lockErr != nil {
			return storage.ExecutionState{}, lockErr
		}
		if keptEntry != "" {
			kept = append(kept, keptEntry)
		}
	}

	payload := map[string]string{}
	if len(kept) > 0 {
		sort.Strings(kept)
		payload["unstopped_owners"] = strings.Join(kept, ",")
	}
	if err := e.appendEvent(ctx, executionID, "", "execution.cancelled", payload); err != nil {
		return storage.ExecutionState{}, err
	}
	cancelled, err := e.Store.LoadExecution(ctx, executionID)
	if err != nil {
		return storage.ExecutionState{}, errors.Join(ownerErr, fmt.Errorf("engine: cancel execution %s: %w", executionID, err))
	}
	if ownerErr != nil {
		return cancelled, &CancelOwnerError{Err: ownerErr}
	}
	return cancelled, nil
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
// rollback covers the base refresh only. A failure in a step after that
// (applyTransitionEffects, the issue.retry_requested event, or resumeIssue)
// is not rolled back. If the Issue is still READY when the failure surfaces,
// it keeps its claimed, reset budget, which the scheduler picks up as a
// normal retry, and RetryIssue reports the failure wrapped in
// RetryStartDeferredError so the caller does not read it as a failed retry.
// resumeIssue's own re-entry can instead advance the Issue past READY (to
// CLAIMED or PREPARING) before failing further in; that leaves the Issue
// mid-resume with no automatic follow-up, so RetryIssue reports it wrapped
// in RetryResumeStuckError instead, naming the operator action needed.
//
// Claiming first also publishes the Issue as READY with no Worker claim for
// the duration of the base refresh, and a concurrent `forge resume` reads
// that shape as resumable. The overlap is safe but not free: both actors
// then work in one Workspace. This whole method runs under IssueLock, which
// rules out that same overlap with CancelExecution (issue 552); a
// concurrent `forge resume` does not take IssueLock and can still see the
// same window.
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
	// The claim, base refresh, transition effects, and resumeIssue below run
	// under IssueLock so a concurrent CancelExecution cannot land between
	// Store.ClaimRetry (which publishes the Issue as READY with no Worker
	// claim) and resumeIssue re-claiming it (issue 552): CancelExecution's
	// per-Issue cancel takes the same lock before it acts.
	var result domain.Issue
	err = e.withIssueLock(ctx, executionID, issueID, func() error {
		claim, err := e.Store.ClaimRetry(ctx, executionID, issueID, domain.NewRetryBudget(failedBudget.Limits()))
		if err != nil {
			return retryClaimError(issueID, issue.State, err)
		}
		if err := e.refreshRetryBase(ctx, state.Execution, issueID); err != nil {
			if abortErr := e.Store.AbortRetry(ctx, executionID, issueID, failedBudget); abortErr != nil {
				return errors.Join(err, abortErr)
			}
			return err
		}
		if err := e.applyTransitionEffects(ctx, executionID, issueID, claim.From, claim.Issue.State); err != nil {
			return &RetryStartDeferredError{Err: err}
		}
		if err := e.appendEvent(ctx, executionID, issueID, "issue.retry_requested", map[string]string{}); err != nil {
			return &RetryStartDeferredError{Err: err}
		}
		resumed, err := e.resumeIssue(ctx, state.Execution, claim.Issue, nil)
		if err != nil {
			return e.deferredOrStuckResumeError(ctx, executionID, issueID, err)
		}
		result = resumed
		return nil
	})
	if err != nil {
		return domain.Issue{}, err
	}
	return result, nil
}

// deferredOrStuckResumeError classifies a resumeIssue failure that surfaces
// during RetryIssue, after the retry claim has already committed.
// resumeIssue's re-entry to a READY Issue (resumeFromReady) claims it and
// transitions it to CLAIMED and then PREPARING before it can fail further
// in, so only a failure that never reaches that code leaves the Issue in
// the READY/unclaimed shape the scheduler already treats as ready to run.
// A failure once resumeIssue has moved the Issue past READY leaves it
// stuck mid-resume instead, which needs an explicit operator-run `forge
// resume`, not automatic scheduler pickup.
func (e *Engine) deferredOrStuckResumeError(ctx context.Context, executionID, issueID string, cause error) error {
	reloaded, reloadErr := e.Store.GetIssue(ctx, executionID, issueID)
	if reloadErr != nil {
		return fmt.Errorf("engine: retry issue %s: resume failed (%w) and reload to classify it also failed: %w", issueID, cause, reloadErr)
	}
	if reloaded.State == domain.StateReady {
		return &RetryStartDeferredError{Err: cause}
	}
	return &RetryResumeStuckError{Err: cause, State: reloaded.State}
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
		return e.reportBaseRefreshFailure(ctx, exec.ID, issueID, "old_base_lookup_failed", "", "", err)
	}
	newBase, err := e.TargetTip.CurrentTip(ctx)
	if err != nil {
		wrapped := fmt.Errorf("engine: resolve target tip for issue %s retry: %w", issueID, err)
		return e.reportBaseRefreshFailure(ctx, exec.ID, issueID, "resolve_target_tip_failed", oldBase, "", wrapped)
	}
	if newBase == "" || newBase == oldBase {
		return nil
	}

	if e.Ancestry != nil {
		ok, err := e.Ancestry.IsAncestor(ctx, oldBase, newBase)
		if err != nil {
			wrapped := fmt.Errorf("engine: verify base refresh for issue %s (%s -> %s): %w", issueID, oldBase, newBase, err)
			return e.reportBaseRefreshFailure(ctx, exec.ID, issueID, "ancestry_check_failed", oldBase, newBase, wrapped)
		}
		if !ok {
			refusal := fmt.Errorf(
				"engine: refusing to refresh issue %s base %s -> %s: %s does not descend from the current base, which would risk dropping a merged dependency",
				issueID, oldBase, newBase, newBase)
			if err := e.appendEvent(ctx, exec.ID, issueID, "worker.base_refresh_refused", map[string]string{
				"old_base": oldBase,
				"new_base": newBase,
				"reason":   "not_descendant",
			}); err != nil {
				return err
			}
			return refusal
		}
	}

	if _, err := e.Workspaces.Validate(ctx, exec.ID, issueID); err == nil {
		rebaser, ok := e.Workspaces.(WorkspaceRebaser)
		if !ok {
			wrapped := fmt.Errorf(
				"engine: cannot refresh base for issue %s: a Workspace exists but Workspaces does not support Rebase", issueID)
			return e.reportBaseRefreshFailure(ctx, exec.ID, issueID, "rebase_unsupported", oldBase, newBase, wrapped)
		}
		conflicts, err := rebaser.Rebase(ctx, exec.ID, issueID, newBase)
		if err != nil {
			wrapped := fmt.Errorf("engine: rebase issue %s workspace onto %s: %w", issueID, newBase, err)
			return e.reportBaseRefreshFailure(ctx, exec.ID, issueID, "rebase_failed", oldBase, newBase, wrapped)
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

// reportBaseRefreshFailure appends a worker.base_refresh_failed event naming
// reason and cause, so a refreshRetryBase fault leaves a trace in the store
// instead of the bare return that left RetryIssue's FAILED Issue
// indistinguishable from one nobody retried. oldBase and newBase are
// omitted from the event when not yet known. It returns cause, or the
// append error if the append itself fails, matching the existing
// worker.base_refresh_conflict convention below.
func (e *Engine) reportBaseRefreshFailure(ctx context.Context, executionID, issueID, reason, oldBase, newBase string, cause error) error {
	data := map[string]string{
		"reason": reason,
		"error":  cause.Error(),
	}
	if oldBase != "" {
		data["old_base"] = oldBase
	}
	if newBase != "" {
		data["new_base"] = newBase
	}
	if err := e.appendEvent(ctx, executionID, issueID, "worker.base_refresh_failed", data); err != nil {
		return err
	}
	return cause
}

// workerOwners groups an Execution's Worker claims by what the Engine knows
// about their owning process. Live holds the owners that passed the identity
// test. Unknown holds the owners the Engine could not inspect, which needs
// its own group because neither default is safe: signalling the pid can hit
// an unrelated process that reused it, and releasing the claim can hand the
// Issue to a second Execution while the original owner still writes to the
// Workspace. Cancel therefore does neither — it keeps the claim and reports
// the owner.
type workerOwners struct {
	live    map[string]int
	unknown map[string]int
}

// keeps reports whether issueID's claim must survive the cancel, and names
// the owner pid it belongs to.
func (o workerOwners) keeps(issueID string, stopped map[int]struct{}) (int, bool) {
	if pid, ok := o.unknown[issueID]; ok {
		return pid, true
	}
	pid, ok := o.live[issueID]
	if !ok {
		return 0, false
	}
	if _, gone := stopped[pid]; gone {
		return 0, false
	}
	return pid, true
}

// workerOwnersOf groups each Issue in executionID by its owner's liveness.
// It drops an Issue whose owner process is gone, whose owner pid the
// operating system reused for an unrelated process, or which has no claim at
// all (ErrNotFound). The error collects the claim reads that failed; the
// returned groups stay usable.
func (e *Engine) workerOwnersOf(ctx context.Context, executionID string, issues []domain.Issue) (workerOwners, error) {
	owners := workerOwners{live: map[string]int{}, unknown: map[string]int{}}
	// One orchestrator normally owns every Issue in the Execution, and the
	// token lookup can run a subprocess, so ask once per pid.
	// The key is pid plus recorded token, because two claim rows can name
	// one pid with different tokens, and only one of them is the live owner.
	tokens := map[string]bool{}
	var errs error
	for _, issue := range issues {
		claim, err := e.Store.WorkerClaim(ctx, executionID, issue.ID)
		if err != nil {
			if !errors.Is(err, storage.ErrNotFound) {
				errs = errors.Join(errs, fmt.Errorf("engine: read worker claim for issue %s: %w", issue.ID, err))
			}
			continue
		}
		key := fmt.Sprintf("%d/%s", claim.OwnerPID, claim.OwnerToken)
		live, cached := tokens[key]
		if !cached {
			live, err = e.claimOwnerIsLive(ctx, claim)
			if err != nil {
				errs = errors.Join(errs, fmt.Errorf("engine: inspect worker owner for issue %s: %w", issue.ID, err))
				owners.unknown[issue.ID] = claim.OwnerPID
				continue
			}
			tokens[key] = live
		}
		if live {
			owners.live[issue.ID] = claim.OwnerPID
		}
	}
	return owners, errs
}

// sortedOwnerPIDs returns each distinct owner pid one time, in a stable
// order, so cancel signals a pid that owns several Issues one time.
func sortedOwnerPIDs(ownerByIssue map[string]int) []int {
	seen := map[int]struct{}{}
	pids := make([]int, 0, len(ownerByIssue))
	for _, pid := range ownerByIssue {
		if _, ok := seen[pid]; ok {
			continue
		}
		seen[pid] = struct{}{}
		pids = append(pids, pid)
	}
	sort.Ints(pids)
	return pids
}
