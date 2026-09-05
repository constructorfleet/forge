package engine

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/Teagan42/forge/internal/storage"
)

// heartbeatInterval is how often a live Worker's last_heartbeat advances.
const heartbeatInterval = 5 * time.Second

// heartbeatStallAfter bounds how long RunWorkerHeartbeat keeps advancing
// last_heartbeat without a fresh WorkerActivity Touch (constructorfleet/
// forge#463). It matches the TUI's staleHeartbeat window (internal/tui/
// frame.go) so a wedged local agent shows Stale within about 15-20 seconds
// of really stalling, long before the adapter's 20-minute default timeout
// fires, instead of a still-ticking wall clock masking the stall as live.
const heartbeatStallAfter = 15 * time.Second

// WorkerActivity tracks the last time real progress was observed for one
// Worker's active agent invocation — a transcript event streamed from the
// running subprocess — as distinct from the claim goroutine merely still
// being alive. Touch and Stalled are safe for concurrent use.
type WorkerActivity struct {
	lastNano atomic.Int64
}

// NewWorkerActivity returns a WorkerActivity considered fresh as of now, so
// a Worker between agent invocations (no transcript activity yet) is never
// treated as already stalled.
func NewWorkerActivity(now time.Time) *WorkerActivity {
	a := &WorkerActivity{}
	a.Touch(now)
	return a
}

// Touch records now as the last time real progress was observed.
func (a *WorkerActivity) Touch(now time.Time) {
	a.lastNano.Store(now.UnixNano())
}

// Stalled reports whether now is more than after past the last Touch.
func (a *WorkerActivity) Stalled(now time.Time, after time.Duration) bool {
	return now.Sub(time.Unix(0, a.lastNano.Load())) > after
}

// LastTouch returns the time of the most recent Touch (or of construction,
// if Touch was never called since).
func (a *WorkerActivity) LastTouch() time.Time {
	return time.Unix(0, a.lastNano.Load())
}

// workerActivityKey identifies the one WorkerActivity tracked per claimed
// Worker, keyed by (executionID, issueID) since an issueID alone is not
// unique across Executions (mirrors semanticSessionKey in semantic.go).
type workerActivityKey struct {
	executionID string
	issueID     string
}

// heartbeatIntervalOrDefault returns e.HeartbeatInterval, or the package
// default heartbeatInterval when unset.
func (e *Engine) heartbeatIntervalOrDefault() time.Duration {
	if e.HeartbeatInterval > 0 {
		return e.HeartbeatInterval
	}
	return heartbeatInterval
}

// heartbeatStallAfterOrDefault returns e.HeartbeatStallAfter, or the package
// default heartbeatStallAfter when unset.
func (e *Engine) heartbeatStallAfterOrDefault() time.Duration {
	if e.HeartbeatStallAfter > 0 {
		return e.HeartbeatStallAfter
	}
	return heartbeatStallAfter
}

// startWorkerActivity creates and registers a fresh WorkerActivity for
// (executionID, issueID), returning it so the caller can hand it straight
// to RunWorkerHeartbeat. Pair with a deferred stopWorkerActivity.
func (e *Engine) startWorkerActivity(executionID, issueID string) *WorkerActivity {
	activity := NewWorkerActivity(e.Now())
	e.workerActivities.Store(workerActivityKey{executionID, issueID}, activity)
	return activity
}

// touchWorkerActivity records progress for (executionID, issueID)'s active
// WorkerActivity, if one is currently tracked. A no-op otherwise — e.g. a
// test that invokes executeAgent directly without ExecuteInExecution ever
// having called startWorkerActivity.
func (e *Engine) touchWorkerActivity(executionID, issueID string) {
	v, ok := e.workerActivities.Load(workerActivityKey{executionID, issueID})
	if !ok {
		return
	}
	v.(*WorkerActivity).Touch(e.Now())
}

// stopWorkerActivity removes (executionID, issueID)'s WorkerActivity once
// its Worker claim is released.
func (e *Engine) stopWorkerActivity(executionID, issueID string) {
	e.workerActivities.Delete(workerActivityKey{executionID, issueID})
}

// keepWorkerActivityFresh touches (executionID, issueID)'s WorkerActivity on
// a ticker for as long as the returned stop func has not been called. It
// brackets a Forge-owned blocking operation that has no per-line output of
// its own to touch on — a Quality Gate command — so a long-but-healthy run
// is not mistaken for a wedged Agent (constructorfleet/forge#463): unlike an
// Agent invocation, Forge trusts its own gate command to eventually finish
// or be caught by its own bounds, so treating it as live for as long as it
// runs is safe. A no-op if nothing is tracked for this key (see
// touchWorkerActivity). Always call the returned stop, typically via a
// bracketing pair around the operation rather than defer, since defer would
// keep it running for the rest of the enclosing function.
func (e *Engine) keepWorkerActivityFresh(ctx context.Context, executionID, issueID string) (stop func()) {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(e.heartbeatStallAfterOrDefault() / 2)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				e.touchWorkerActivity(executionID, issueID)
			}
		}
	}()
	return func() { close(done) }
}

// HeartbeatStallPolicy bundles RunWorkerHeartbeat's two stall-detection
// params — the WorkerActivity to watch and how long it may go untouched
// before a beat is withheld — so call sites pass one self-documenting value
// instead of two same-typed trailing positional args that are easy to
// mis-order or leave nil/zero by mistake.
type HeartbeatStallPolicy struct {
	// Activity is the WorkerActivity RunWorkerHeartbeat watches. A nil
	// Activity preserves the prior always-beats behavior; production callers
	// always pass a non-nil Activity for the whole claim-to-release
	// lifecycle, so a phase with no per-progress signal of its own (claim,
	// quality gates, commit, push, PR creation) instead brackets that phase
	// with keepWorkerActivityFresh, which touches the same shared Activity
	// on a ticker.
	Activity *WorkerActivity
	// After is how long Activity may go without a Touch before
	// RunWorkerHeartbeat withholds a beat.
	After time.Duration
}

// RunWorkerHeartbeat beats Store.HeartbeatWorker every interval while ctx is
// live, stamping the Worker claim with now(). When stall.Activity is
// non-nil, a beat is withheld once it has gone stall.After without a Touch,
// so last_heartbeat freezes rather than a wall-clock tick masking a wedged
// agent as live (constructorfleet/forge#463). It blocks until ctx is done
// and stops silently if the claim is gone (ErrNotFound).
func RunWorkerHeartbeat(ctx context.Context, store storage.Store, executionID, issueID string, interval time.Duration, now func() time.Time, stall HeartbeatStallPolicy) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t := now()
			if stall.Activity != nil && stall.Activity.Stalled(t, stall.After) {
				continue
			}
			if err := store.HeartbeatWorker(ctx, executionID, issueID, t); err != nil {
				return
			}
		}
	}
}
