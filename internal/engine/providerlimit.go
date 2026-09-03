package engine

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
)

// handleProviderLimit implements the StatusProviderLimit arm of
// executeAgent's result switch (issue 423): the Agent stopped because the
// model provider applied a rate or quota limit.
//
// A provider limit is an external transient condition, not a defect in the
// Agent's work, so Forge waits instead of repairing. The Issue always parks
// in PROVIDER_LIMIT first, which records the true cause in the Issue's state
// history. The stop then counts against the provider-limit retry budget,
// which is independent of the gate, review, and CI budgets (ADR 0007):
//
//   - While the budget still has room, the Issue rests in PROVIDER_LIMIT and
//     gets a backoff deadline. ProviderLimitController returns it to READY
//     once that time passes.
//   - Once this stop exhausts the budget, the Issue moves on to FAILED at
//     once. Forge does not schedule a wait it can never spend. A reader of
//     the state history then sees IMPLEMENTING -> PROVIDER_LIMIT -> FAILED,
//     which names the provider limit as the cause of the terminal state.
//
// A ceiling of N therefore tolerates N provider-limit stops for one Issue,
// and the Nth stop is terminal.
//
// The Workspace is not removed here. Uncommitted changes are discarded first
// so the automatic retry can refresh the base without a dirty worktree
// precondition failure. The Worker claim is released, the same way
// handleNeedsInfo releases it, so the retry can claim the Issue again.
func (e *Engine) handleProviderLimit(ctx context.Context, executionID, issueID, workerRef string, result agent.AgentResult) (domain.Issue, error) {
	e.discardWorkspaceChanges(ctx, executionID, issueID)

	issue, err := e.transition(ctx, executionID, issueID, domain.StateProviderLimit)
	if err != nil {
		return domain.Issue{}, err
	}

	recordErr := issue.RecordProviderLimitStop()
	var exhausted *domain.RetryExhaustedError
	if recordErr != nil && !errors.As(recordErr, &exhausted) {
		return domain.Issue{}, fmt.Errorf("engine: record provider-limit stop for issue %s: %w", issueID, recordErr)
	}
	if err := e.Store.UpdateRetryBudget(ctx, executionID, issueID, issue.RetryBudget); err != nil {
		return domain.Issue{}, fmt.Errorf("engine: persist provider-limit budget for issue %s: %w", issueID, err)
	}

	if err := e.releaseProviderLimitWorker(ctx, executionID, issueID, workerRef); err != nil {
		return domain.Issue{}, err
	}

	// The budget is out of room either because this stop landed past the
	// ceiling (recordErr) or because this stop reached it. Both mean no
	// further attempt is allowed, so the Issue fails now rather than waiting
	// out a backoff it can never spend.
	if exhausted != nil || issue.RetryBudget.ProviderLimitExhausted() {
		if err := e.appendEvent(ctx, executionID, issueID, "issue.provider_limit_exhausted", map[string]string{
			"limit":   strconv.Itoa(issue.RetryBudget.Limits().ProviderLimit),
			"stops":   strconv.Itoa(issue.RetryBudget.ProviderLimitFailures()),
			"summary": result.Summary,
		}); err != nil {
			return domain.Issue{}, err
		}
		return e.transition(ctx, executionID, issueID, domain.StateFailed)
	}

	attempt := issue.RetryBudget.ProviderLimitFailures()
	backoff := domain.ProviderLimitBackoff(attempt)
	retryAt := e.Now().Add(backoff).UTC()
	if err := e.Store.ScheduleProviderLimitRetry(ctx, executionID, issueID, &retryAt); err != nil {
		return domain.Issue{}, fmt.Errorf("engine: schedule provider-limit retry for issue %s: %w", issueID, err)
	}
	issue.ProviderLimitRetryAt = &retryAt

	if err := e.appendEvent(ctx, executionID, issueID, "issue.provider_limit", map[string]string{
		"attempt":   strconv.Itoa(attempt),
		"remaining": strconv.Itoa(issue.RetryBudget.RemainingProviderLimit()),
		"backoff":   backoff.String(),
		"retry_at":  retryAt.Format(time.RFC3339),
		"summary":   result.Summary,
	}); err != nil {
		return domain.Issue{}, err
	}
	return issue, nil
}

// releaseProviderLimitWorker frees the Worker slot for a parked Issue. It
// mirrors handleNeedsInfo: the Event models the release, and the claim is
// dropped so the automatic retry can claim the Issue again.
func (e *Engine) releaseProviderLimitWorker(ctx context.Context, executionID, issueID, workerRef string) error {
	if err := e.appendEvent(ctx, executionID, issueID, "worker.released", map[string]string{
		"worker_ref": workerRef,
	}); err != nil {
		return err
	}
	if err := e.Store.ReleaseWorkerClaim(ctx, executionID, issueID); err != nil {
		return fmt.Errorf("engine: release worker claim for issue %s: %w", issueID, err)
	}
	return nil
}
