package engine_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
)

// TestRunWorkerHeartbeatAdvancesLastHeartbeat pins the heartbeat helper:
// while a Worker claim is active, the ticker repeatedly advances
// workers.last_heartbeat via Store.HeartbeatWorker with the supplied clock,
// and stops once the context is cancelled.
func TestRunWorkerHeartbeatAdvancesLastHeartbeat(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	if err := store.CreateExecution(ctx, domain.Execution{ID: "exec-hb", BaseRevision: "base", StartedAt: time.Now()}); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	if err := store.CreateIssue(ctx, domain.Issue{
		ID: "issue-hb", ExecutionID: "exec-hb", State: domain.StateClaimed, Scope: domain.ScopeManaged,
		RetryBudget: domain.NewRetryBudget(domain.RetryLimits{Gate: 1, Review: 1, CI: 1}),
	}); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.ClaimIssue(ctx, "exec-hb", "issue-hb", "worker-hb"); err != nil {
		t.Fatalf("ClaimIssue: %v", err)
	}

	// Deterministic clock: seconds advance each tick regardless of wall time.
	var ticks int64
	clock := func() time.Time {
		n := atomic.AddInt64(&ticks, 1)
		return time.Unix(n, 0).UTC()
	}

	heartbeatCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		engine.RunWorkerHeartbeat(heartbeatCtx, store, "exec-hb", "issue-hb", 10*time.Millisecond, clock)
		close(done)
	}()

	// Let several ticks land before stopping.
	time.Sleep(60 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunWorkerHeartbeat did not stop after cancel")
	}

	claim, err := store.WorkerClaim(ctx, "exec-hb", "issue-hb")
	if err != nil {
		t.Fatalf("WorkerClaim: %v", err)
	}
	if claim.LastHeartbeat.Unix() < 1 {
		t.Fatalf("LastHeartbeat = %v, want >1 tick to have landed", claim.LastHeartbeat)
	}
	if atomic.LoadInt64(&ticks) < 2 {
		t.Fatalf("expected multiple beats ticked, got %d", ticks)
	}
}
