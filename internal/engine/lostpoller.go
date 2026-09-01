package engine

import (
	"context"
	"time"

	"github.com/Teagan42/forge/internal/storage"
)

// LostRecoveryLister is LostRecoveryStore plus the capability to enumerate
// every active ExecutionLease, so RecoverAllLostExecutions can check every
// in-flight remote execution without a caller naming Execution/Issue IDs up
// front (issue #400: no caller drove RecoverLostExecution outside its own
// test, so LOST detection never triggered in production).
type LostRecoveryLister interface {
	LostRecoveryStore
	ListActiveExecutionLeases(ctx context.Context) ([]storage.ExecutionLease, error)
}

// LostRecoveryEntry is one lapsed lease RecoverAllLostExecutions acted on:
// the Execution/Issue it belongs to, RecoverLostExecution's result, and any
// error RecoverLostExecution returned (e.g. *domain.RetryExhaustedError).
type LostRecoveryEntry struct {
	ExecutionID string
	IssueID     string
	Result      LostRecoveryResult
	Err         error
}

// RecoverAllLostExecutions lists every active ExecutionLease and runs
// RecoverLostExecution against each one. It returns one LostRecoveryEntry
// per lease RecoverLostExecution found lost (LostRecoveryResult.Lost true),
// including ones whose retry budget was exhausted (Err set, per
// RecoverLostExecution's contract); a lease whose heartbeat had not lapsed
// contributes nothing to the result, since there is nothing to report. A
// single lease's RecoverLostExecution error never stops the pass over the
// rest — each lease is independent, exactly like Scheduler.Run isolates one
// Issue's error from its siblings.
func RecoverAllLostExecutions(ctx context.Context, store LostRecoveryLister, now func() time.Time) ([]LostRecoveryEntry, error) {
	leases, err := store.ListActiveExecutionLeases(ctx)
	if err != nil {
		return nil, err
	}

	entries := make([]LostRecoveryEntry, 0)
	for _, lease := range leases {
		result, err := RecoverLostExecution(ctx, store, lease.ExecutionID, lease.IssueID, now)
		if !result.Lost {
			continue
		}
		entries = append(entries, LostRecoveryEntry{
			ExecutionID: lease.ExecutionID,
			IssueID:     lease.IssueID,
			Result:      result,
			Err:         err,
		})
	}
	return entries, nil
}

// RunLostRecoveryLoop runs RecoverAllLostExecutions once immediately, then
// again every interval, reporting each pass's results (and any listing
// error) to onTick, until ctx is cancelled. This is the periodic/scheduling
// loop issue #400 asks for: the controller-side counterpart to a Worker's
// heartbeat, wired in by cmd/forge alongside a `forge execute` run against
// the Remote ExecutionBackend so a vanished Worker is detected even when no
// WorkerClient call is in flight to trigger recovery reactively (see
// internal/execution/remote.Backend's classifyErr).
func RunLostRecoveryLoop(ctx context.Context, store LostRecoveryLister, now func() time.Time, interval time.Duration, onTick func(results []LostRecoveryEntry, err error)) {
	runOnce := func() {
		results, err := RecoverAllLostExecutions(ctx, store, now)
		onTick(results, err)
	}

	runOnce()
	if ctx.Err() != nil {
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runOnce()
		}
	}
}
