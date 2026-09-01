package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/storage"
)

// TestRecoverAllLostExecutions checks every active ExecutionLease and
// recovers the ones whose heartbeat has lapsed, leaving the rest untouched
// — the single-pass building block a periodic loop (issue #400) calls on
// each tick, so it never needs to know Execution/Issue IDs in advance.
func TestRecoverAllLostExecutions(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	lapsedExpiry := time.Now().Add(-time.Minute)
	seedLostRecoveryFixture(t, store, "exec-1", "issue-lost", domain.StateImplementing, 3, lapsedExpiry)

	freshExpiry := time.Now().Add(time.Minute)
	seedLostRecoveryFixture(t, store, "exec-2", "issue-alive", domain.StateImplementing, 3, freshExpiry)

	results, err := engine.RecoverAllLostExecutions(ctx, store, time.Now)
	if err != nil {
		t.Fatalf("RecoverAllLostExecutions: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 recovery result (only the lapsed lease), got %d: %+v", len(results), results)
	}
	got := results[0]
	if got.ExecutionID != "exec-1" || got.IssueID != "issue-lost" {
		t.Fatalf("recovered wrong lease: %+v", got)
	}
	if !got.Result.Lost || !got.Result.Retried {
		t.Fatalf("expected issue-lost to be Lost and Retried, got %+v", got.Result)
	}

	// The still-fresh lease was left alone.
	if _, err := store.ExecutionLease(ctx, "exec-2", "issue-alive"); err != nil {
		t.Fatalf("expected issue-alive lease to remain held, got %v", err)
	}
}

func TestRecoverAllLostExecutions_NoActiveLeases_EmptyResult(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	results, err := engine.RecoverAllLostExecutions(ctx, store, time.Now)
	if err != nil {
		t.Fatalf("RecoverAllLostExecutions: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no results, got %+v", results)
	}
}

// fakeLister composes a real *storage.SQLiteStore's other methods with a
// canned ListActiveExecutionLeases return, so RunLostRecoveryLoop's ticking
// behavior can be tested without depending on real lease timing.
type fakeLister struct {
	*storage.SQLiteStore
	leases []storage.ExecutionLease
}

func (f *fakeLister) ListActiveExecutionLeases(ctx context.Context) ([]storage.ExecutionLease, error) {
	return f.leases, nil
}

// TestRunLostRecoveryLoop_TicksUntilContextCancelled checks that the loop
// calls RecoverAllLostExecutions on every tick and stops as soon as ctx is
// cancelled, reporting each tick's results through onTick — the periodic
// scheduling wiring issue #400 asks for.
func TestRunLostRecoveryLoop_TicksUntilContextCancelled(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	lapsedExpiry := time.Now().Add(-time.Minute)
	seedLostRecoveryFixture(t, store, "exec-1", "issue-lost", domain.StateImplementing, 3, lapsedExpiry)
	lease, err := store.ExecutionLease(ctx, "exec-1", "issue-lost")
	if err != nil {
		t.Fatalf("ExecutionLease: %v", err)
	}
	lister := &fakeLister{SQLiteStore: store, leases: []storage.ExecutionLease{lease}}

	runCtx, cancel := context.WithCancel(ctx)
	tickCount := 0
	done := make(chan struct{})

	go func() {
		engine.RunLostRecoveryLoop(runCtx, lister, time.Now, time.Millisecond, func(results []engine.LostRecoveryEntry, err error) {
			tickCount++
			if tickCount == 1 {
				cancel()
			}
		})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunLostRecoveryLoop did not return after context cancellation")
	}

	if tickCount == 0 {
		t.Fatal("expected at least one tick before cancellation")
	}
}
