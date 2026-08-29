package planengine_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/planengine"
	"github.com/Teagan42/forge/internal/storage"
)

func openTestStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "forge.db")
	store, err := storage.Open(dsn)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return store
}

var testExecutionSeq int

func newTestRuntime(store *storage.SQLiteStore) *planengine.Runtime {
	r := planengine.New(store)
	r.NewExecutionID = func() string {
		testExecutionSeq++
		return fmt.Sprintf("plan-exec-%d", testExecutionSeq)
	}
	r.Now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	r.OwnerPID = func() int { return 100 }
	r.ProcessRunning = func(pid int) (bool, error) { return false, nil }
	return r
}

func TestStartCreatesFreshPlanningExecution(t *testing.T) {
	store := openTestStore(t)
	r := newTestRuntime(store)

	exec, err := r.Start(context.Background(), "feature-1", "base-rev")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if exec.FeatureID != "feature-1" || exec.BaseRevision != "base-rev" {
		t.Fatalf("unexpected execution: %+v", exec)
	}
	if exec.Status != domain.PlanningStatusActive {
		t.Fatalf("expected ACTIVE status, got %s", exec.Status)
	}

	lease, err := store.FeaturePlanningLease(context.Background(), "feature-1")
	if err != nil {
		t.Fatalf("FeaturePlanningLease: %v", err)
	}
	if lease.ExecutionID != exec.ID {
		t.Fatalf("expected lease to reference %s, got %s", exec.ID, lease.ExecutionID)
	}
	if lease.OwnerPID != 100 {
		t.Fatalf("expected owner pid 100, got %d", lease.OwnerPID)
	}
}

func TestStartRejectsWhileLiveProcessOwnsLease(t *testing.T) {
	store := openTestStore(t)
	r := newTestRuntime(store)

	first, err := r.Start(context.Background(), "feature-1", "base-rev")
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}

	other := newTestRuntime(store)
	other.OwnerPID = func() int { return 200 }
	other.ProcessRunning = func(pid int) (bool, error) { return true, nil } // pid 100 still alive

	_, err = other.Start(context.Background(), "feature-1", "base-rev")
	if err == nil {
		t.Fatal("expected an error starting planning while a live process owns the lease")
	}
	var conflict *storage.PlanningLeaseConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected *storage.PlanningLeaseConflictError, got %v", err)
	}
	if conflict.OwningExecutionID != first.ID {
		t.Fatalf("expected conflict to reference %s, got %s", first.ID, conflict.OwningExecutionID)
	}
}

func TestStartReclaimsAbandonedLease(t *testing.T) {
	store := openTestStore(t)
	r := newTestRuntime(store)

	first, err := r.Start(context.Background(), "feature-1", "base-rev")
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}

	resumer := newTestRuntime(store)
	resumer.OwnerPID = func() int { return 999 }
	resumer.ProcessRunning = func(pid int) (bool, error) { return false, nil } // pid 100 is dead

	resumed, err := resumer.Start(context.Background(), "feature-1", "base-rev")
	if err != nil {
		t.Fatalf("resume Start: %v", err)
	}
	if resumed.ID != first.ID {
		t.Fatalf("expected resumed execution to reuse %s, got %s", first.ID, resumed.ID)
	}

	lease, err := store.FeaturePlanningLease(context.Background(), "feature-1")
	if err != nil {
		t.Fatalf("FeaturePlanningLease: %v", err)
	}
	if lease.OwnerPID != 999 {
		t.Fatalf("expected lease reclaimed by pid 999, got %d", lease.OwnerPID)
	}
}

func TestStartTreatsOwnRestartAsReclaim(t *testing.T) {
	store := openTestStore(t)
	r := newTestRuntime(store)

	first, err := r.Start(context.Background(), "feature-1", "base-rev")
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}

	// Same process ID restarting: ProcessRunning would report itself alive,
	// but since OwnerPID matches, it's a restart, not a foreign live owner.
	restarted := newTestRuntime(store)
	restarted.OwnerPID = func() int { return 100 }
	restarted.ProcessRunning = func(pid int) (bool, error) { return true, nil }

	exec, err := restarted.Start(context.Background(), "feature-1", "base-rev")
	if err != nil {
		t.Fatalf("restart Start: %v", err)
	}
	if exec.ID != first.ID {
		t.Fatalf("expected restart to reuse %s, got %s", first.ID, exec.ID)
	}
}

func TestStartAfterFinishStartsFreshExecution(t *testing.T) {
	store := openTestStore(t)
	r := newTestRuntime(store)

	first, err := r.Start(context.Background(), "feature-1", "base-rev")
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := r.Finish(context.Background(), "feature-1", first.ID, domain.PlanningStatusComplete); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	second, err := r.Start(context.Background(), "feature-1", "base-rev-2")
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if second.ID == first.ID {
		t.Fatal("expected a fresh execution after Finish, got the same ID")
	}

	if _, err := store.FeaturePlanningLease(context.Background(), "feature-1"); err != nil {
		t.Fatalf("expected an active lease for the fresh execution: %v", err)
	}
}

func TestStartReleasesLeaseFromTerminalExecutionStillHeld(t *testing.T) {
	store := openTestStore(t)
	r := newTestRuntime(store)

	first, err := r.Start(context.Background(), "feature-1", "base-rev")
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	// Mark the execution terminal without releasing the lease directly
	// (simulating a crash between UpdatePlanningStatus and lease release).
	if err := store.UpdatePlanningStatus(context.Background(), first.ID, domain.PlanningStatusFailed); err != nil {
		t.Fatalf("UpdatePlanningStatus: %v", err)
	}

	resumer := newTestRuntime(store)
	resumer.OwnerPID = func() int { return 999 }
	resumer.ProcessRunning = func(pid int) (bool, error) { return false, nil }

	second, err := resumer.Start(context.Background(), "feature-1", "base-rev-2")
	if err != nil {
		t.Fatalf("Start after terminal: %v", err)
	}
	if second.ID == first.ID {
		t.Fatal("expected a fresh execution once the prior one was terminal")
	}
	if second.Status != domain.PlanningStatusActive {
		t.Fatalf("expected fresh execution to be ACTIVE, got %s", second.Status)
	}
}

func TestFinishReleasesLeaseAndPersistsStatus(t *testing.T) {
	store := openTestStore(t)
	r := newTestRuntime(store)

	exec, err := r.Start(context.Background(), "feature-1", "base-rev")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := r.Finish(context.Background(), "feature-1", exec.ID, domain.PlanningStatusNeedsHuman); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	loaded, err := store.LoadPlanningExecution(context.Background(), exec.ID)
	if err != nil {
		t.Fatalf("LoadPlanningExecution: %v", err)
	}
	if loaded.Status != domain.PlanningStatusNeedsHuman {
		t.Fatalf("expected NEEDS_HUMAN, got %s", loaded.Status)
	}
	if _, err := store.FeaturePlanningLease(context.Background(), "feature-1"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected lease released, got %v", err)
	}
}
