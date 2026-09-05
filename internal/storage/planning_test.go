package storage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
)

func TestCreateAndLoadPlanningExecution(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	exec := domain.PlanningExecution{
		ID:           "plan-exec-1",
		FeatureID:    "feature-1",
		BaseRevision: "abc123",
		Status:       domain.PlanningStatusActive,
		StartedAt:    time.Now(),
	}
	if err := store.CreatePlanningExecution(ctx, exec); err != nil {
		t.Fatalf("CreatePlanningExecution: %v", err)
	}

	loaded, err := store.LoadPlanningExecution(ctx, exec.ID)
	if err != nil {
		t.Fatalf("LoadPlanningExecution: %v", err)
	}
	if loaded.ID != exec.ID || loaded.FeatureID != exec.FeatureID || loaded.BaseRevision != exec.BaseRevision || loaded.Status != exec.Status {
		t.Fatalf("loaded execution mismatch: got %+v, want %+v", loaded, exec)
	}
}

func TestLoadPlanningExecutionNotFound(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	if _, err := store.LoadPlanningExecution(ctx, "missing"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestListPlanningExecutionsByFeature(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	base := time.Now()
	execs := []domain.PlanningExecution{
		{ID: "plan-a1", FeatureID: "feature-a", BaseRevision: "rev1", Status: domain.PlanningStatusActive, StartedAt: base},
		{ID: "plan-a2", FeatureID: "feature-a", BaseRevision: "rev2", Status: domain.PlanningStatusComplete, StartedAt: base.Add(time.Minute)},
		{ID: "plan-b1", FeatureID: "feature-b", BaseRevision: "rev3", Status: domain.PlanningStatusActive, StartedAt: base},
	}
	for _, e := range execs {
		if err := store.CreatePlanningExecution(ctx, e); err != nil {
			t.Fatalf("CreatePlanningExecution(%s): %v", e.ID, err)
		}
	}

	got, err := store.ListPlanningExecutionsByFeature(ctx, "feature-a")
	if err != nil {
		t.Fatalf("ListPlanningExecutionsByFeature: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 executions for feature-a, got %d", len(got))
	}
	if got[0].ID != "plan-a1" || got[1].ID != "plan-a2" {
		t.Fatalf("expected ordering by started_at, got %+v", got)
	}
}

func TestUpdatePlanningStatus(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	exec := domain.PlanningExecution{ID: "plan-status", FeatureID: "feature-1", BaseRevision: "abc", Status: domain.PlanningStatusActive, StartedAt: time.Now()}
	if err := store.CreatePlanningExecution(ctx, exec); err != nil {
		t.Fatalf("CreatePlanningExecution: %v", err)
	}

	if err := store.UpdatePlanningStatus(ctx, exec.ID, domain.PlanningStatusNeedsApproval); err != nil {
		t.Fatalf("UpdatePlanningStatus: %v", err)
	}
	loaded, err := store.LoadPlanningExecution(ctx, exec.ID)
	if err != nil {
		t.Fatalf("LoadPlanningExecution: %v", err)
	}
	if loaded.Status != domain.PlanningStatusNeedsApproval {
		t.Fatalf("expected status NEEDS_APPROVAL, got %s", loaded.Status)
	}
}

func TestUpdatePlanningStatusNotFound(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	if err := store.UpdatePlanningStatus(ctx, "missing", domain.PlanningStatusFailed); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func createPlanningExecution(t *testing.T, store *storage.SQLiteStore, id, featureID string) domain.PlanningExecution {
	t.Helper()
	exec := domain.PlanningExecution{ID: id, FeatureID: featureID, BaseRevision: "abc", Status: domain.PlanningStatusActive, StartedAt: time.Now()}
	if err := store.CreatePlanningExecution(context.Background(), exec); err != nil {
		t.Fatalf("CreatePlanningExecution: %v", err)
	}
	return exec
}

func TestClaimFeaturePlanningLease(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	exec := createPlanningExecution(t, store, "plan-lease-1", "feature-1")
	if err := store.ClaimFeaturePlanningLease(ctx, exec.FeatureID, exec.ID); err != nil {
		t.Fatalf("ClaimFeaturePlanningLease: %v", err)
	}

	lease, err := store.FeaturePlanningLease(ctx, exec.FeatureID)
	if err != nil {
		t.Fatalf("FeaturePlanningLease: %v", err)
	}
	if lease.ExecutionID != exec.ID || lease.FeatureID != exec.FeatureID {
		t.Fatalf("lease mismatch: got %+v", lease)
	}
}

func TestClaimFeaturePlanningLeaseConflict(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	exec1 := createPlanningExecution(t, store, "plan-lease-a", "feature-1")
	exec2 := createPlanningExecution(t, store, "plan-lease-b", "feature-1")

	if err := store.ClaimFeaturePlanningLease(ctx, exec1.FeatureID, exec1.ID); err != nil {
		t.Fatalf("first claim: %v", err)
	}

	err := store.ClaimFeaturePlanningLease(ctx, exec2.FeatureID, exec2.ID)
	if !errors.Is(err, storage.ErrPlanningLeaseHeld) {
		t.Fatalf("expected ErrPlanningLeaseHeld, got %v", err)
	}
	var conflict *storage.PlanningLeaseConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected *PlanningLeaseConflictError, got %T: %v", err, err)
	}
	if conflict.OwningExecutionID != exec1.ID {
		t.Fatalf("expected owning execution %s, got %s", exec1.ID, conflict.OwningExecutionID)
	}
}

func TestFeaturePlanningLeaseNotFound(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	if _, err := store.FeaturePlanningLease(ctx, "missing-feature"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestUpdatePlanningLeaseOwner(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	exec := createPlanningExecution(t, store, "plan-lease-owner", "feature-1")
	if err := store.ClaimFeaturePlanningLease(ctx, exec.FeatureID, exec.ID); err != nil {
		t.Fatalf("ClaimFeaturePlanningLease: %v", err)
	}

	if err := store.UpdatePlanningLeaseOwner(ctx, exec.FeatureID, 4242, "start-4242"); err != nil {
		t.Fatalf("UpdatePlanningLeaseOwner: %v", err)
	}
	lease, err := store.FeaturePlanningLease(ctx, exec.FeatureID)
	if err != nil {
		t.Fatalf("FeaturePlanningLease: %v", err)
	}
	if lease.OwnerPID != 4242 {
		t.Fatalf("expected owner pid 4242, got %d", lease.OwnerPID)
	}
	if lease.OwnerToken != "start-4242" {
		t.Fatalf("expected owner token %q, got %q", "start-4242", lease.OwnerToken)
	}
}

func TestUpdatePlanningLeaseOwnerNotFound(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	if err := store.UpdatePlanningLeaseOwner(ctx, "missing-feature", 1, "token"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestReleaseFeaturePlanningLease(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	exec := createPlanningExecution(t, store, "plan-lease-release", "feature-1")
	if err := store.ClaimFeaturePlanningLease(ctx, exec.FeatureID, exec.ID); err != nil {
		t.Fatalf("ClaimFeaturePlanningLease: %v", err)
	}
	if err := store.ReleaseFeaturePlanningLease(ctx, exec.FeatureID); err != nil {
		t.Fatalf("ReleaseFeaturePlanningLease: %v", err)
	}
	if _, err := store.FeaturePlanningLease(ctx, exec.FeatureID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after release, got %v", err)
	}

	// Releasing an already-released (or never-claimed) lease is a no-op.
	if err := store.ReleaseFeaturePlanningLease(ctx, exec.FeatureID); err != nil {
		t.Fatalf("ReleaseFeaturePlanningLease (no-op): %v", err)
	}
}

func TestReleaseThenReclaimFeaturePlanningLease(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	exec1 := createPlanningExecution(t, store, "plan-lease-reclaim-a", "feature-1")
	exec2 := createPlanningExecution(t, store, "plan-lease-reclaim-b", "feature-1")

	if err := store.ClaimFeaturePlanningLease(ctx, exec1.FeatureID, exec1.ID); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if err := store.ReleaseFeaturePlanningLease(ctx, exec1.FeatureID); err != nil {
		t.Fatalf("release: %v", err)
	}
	if err := store.ClaimFeaturePlanningLease(ctx, exec2.FeatureID, exec2.ID); err != nil {
		t.Fatalf("reclaim after release: %v", err)
	}
	lease, err := store.FeaturePlanningLease(ctx, exec2.FeatureID)
	if err != nil {
		t.Fatalf("FeaturePlanningLease: %v", err)
	}
	if lease.ExecutionID != exec2.ID {
		t.Fatalf("expected reclaimed lease to be owned by %s, got %s", exec2.ID, lease.ExecutionID)
	}
}
