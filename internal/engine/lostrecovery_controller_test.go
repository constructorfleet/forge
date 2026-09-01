package engine_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/storage"
)

// fakeExecutionResumer records every ResumeExecution call, standing in for
// *engine.Engine — a narrow fake for the same reason fakeCIWaiter stands in
// for the CI Supervisor in recovery_test.go.
type fakeExecutionResumer struct {
	calls []string
	err   error
}

func (f *fakeExecutionResumer) ResumeExecution(_ context.Context, executionID string) (storage.ExecutionState, error) {
	f.calls = append(f.calls, executionID)
	if f.err != nil {
		return storage.ExecutionState{}, f.err
	}
	return storage.ExecutionState{}, nil
}

func TestLostExecutionControllerReconcileOnceRedispatchesRetriedExecution(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	expiresAt := time.Now().Add(time.Minute)
	seedLostRecoveryFixture(t, store, "exec-1", "issue-1", domain.StateImplementing, 3, expiresAt)

	now := expiresAt.Add(time.Second)
	resumer := &fakeExecutionResumer{}
	controller := &engine.LostExecutionController{
		Store:   store,
		Leases:  store,
		Resumer: resumer,
		Now:     func() time.Time { return now },
	}

	results, err := controller.ReconcileOnce(ctx)
	if err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %+v", results)
	}
	if !results[0].Lost || !results[0].Retried {
		t.Fatalf("expected Lost+Retried result, got %+v", results[0])
	}
	if len(resumer.calls) != 1 || resumer.calls[0] != "exec-1" {
		t.Fatalf("expected ResumeExecution to be called once for exec-1, got %+v", resumer.calls)
	}
}

func TestLostExecutionControllerReconcileOnceHeartbeatPresentDoesNotRedispatch(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	expiresAt := time.Now().Add(time.Minute)
	seedLostRecoveryFixture(t, store, "exec-1", "issue-1", domain.StateImplementing, 3, expiresAt)

	now := expiresAt.Add(-time.Second)
	resumer := &fakeExecutionResumer{}
	controller := &engine.LostExecutionController{
		Store:   store,
		Leases:  store,
		Resumer: resumer,
		Now:     func() time.Time { return now },
	}

	results, err := controller.ReconcileOnce(ctx)
	if err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %+v", results)
	}
	if results[0].Lost || results[0].Retried {
		t.Fatalf("expected no-op result, got %+v", results[0])
	}
	if len(resumer.calls) != 0 {
		t.Fatalf("expected no ResumeExecution calls, got %+v", resumer.calls)
	}
}

func TestLostExecutionControllerReconcileOnceBudgetExhaustedDoesNotFailPass(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	expiresAt := time.Now().Add(time.Minute)
	seedLostRecoveryFixture(t, store, "exec-1", "issue-1", domain.StateImplementing, 0, expiresAt)

	now := expiresAt.Add(time.Second)
	resumer := &fakeExecutionResumer{}
	controller := &engine.LostExecutionController{
		Store:   store,
		Leases:  store,
		Resumer: resumer,
		Now:     func() time.Time { return now },
	}

	results, err := controller.ReconcileOnce(ctx)
	if err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %+v", results)
	}
	if !results[0].Lost || results[0].Retried {
		t.Fatalf("expected Lost without Retried, got %+v", results[0])
	}
	if len(resumer.calls) != 0 {
		t.Fatalf("expected no ResumeExecution calls when budget exhausted, got %+v", resumer.calls)
	}
}

func TestLostExecutionControllerReconcileOnceContinuesAfterResumerError(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	expiresAt := time.Now().Add(time.Minute)
	seedLostRecoveryFixture(t, store, "exec-1", "issue-1", domain.StateImplementing, 3, expiresAt)
	seedLostRecoveryFixture(t, store, "exec-2", "issue-2", domain.StateImplementing, 3, expiresAt)

	now := expiresAt.Add(time.Second)
	resumeErr := errors.New("resume boom")
	resumer := &fakeExecutionResumer{err: resumeErr}
	controller := &engine.LostExecutionController{
		Store:   store,
		Leases:  store,
		Resumer: resumer,
		Now:     func() time.Time { return now },
	}

	results, err := controller.ReconcileOnce(ctx)
	if err == nil {
		t.Fatalf("expected an aggregated error from the failing resumer")
	}
	if !errors.Is(err, resumeErr) {
		t.Fatalf("expected wrapped resumeErr, got %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected both leases to still be recovered, got %+v", results)
	}
	if len(resumer.calls) != 2 {
		t.Fatalf("expected ResumeExecution attempted for both executions, got %+v", resumer.calls)
	}
}

func TestLostExecutionControllerRunReconcilesUntilContextCancelled(t *testing.T) {
	store := openTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	expiresAt := time.Now().Add(time.Minute)
	seedLostRecoveryFixture(t, store, "exec-1", "issue-1", domain.StateImplementing, 3, expiresAt)

	now := expiresAt.Add(time.Second)
	resumer := &fakeExecutionResumer{}
	controller := &engine.LostExecutionController{
		Store:   store,
		Leases:  store,
		Resumer: resumer,
		Now:     func() time.Time { return now },
	}

	sleepCalls := 0
	controller.Sleep = func(ctx context.Context, _ time.Duration) error {
		sleepCalls++
		if sleepCalls >= 2 {
			cancel()
		}
		return ctx.Err()
	}

	err := controller.Run(ctx, time.Millisecond, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if sleepCalls != 2 {
		t.Fatalf("expected 2 Sleep calls, got %d", sleepCalls)
	}
	// The first pass retried the lease and redispatched it; ReleaseExecutionLease
	// took the lease out of ListActiveExecutionLeases, so the second pass finds
	// nothing left to recover.
	if len(resumer.calls) != 1 || resumer.calls[0] != "exec-1" {
		t.Fatalf("expected exactly one redispatch, got %+v", resumer.calls)
	}
}
