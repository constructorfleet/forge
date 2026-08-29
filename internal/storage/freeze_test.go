package storage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/storage"
)

func TestFeatureFreezeLifecycle(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	frozen, _, err := store.IsFeatureFrozen(ctx, "feature-1")
	if err != nil {
		t.Fatalf("IsFeatureFrozen: %v", err)
	}
	if frozen {
		t.Fatal("a Feature with no freeze row must not report frozen")
	}

	if err := store.FreezeFeature(ctx, "feature-1", "plan contradicts the auth model", "42"); err != nil {
		t.Fatalf("FreezeFeature: %v", err)
	}

	frozen, freeze, err := store.IsFeatureFrozen(ctx, "feature-1")
	if err != nil {
		t.Fatalf("IsFeatureFrozen: %v", err)
	}
	if !frozen {
		t.Fatal("FreezeFeature did not take effect")
	}
	if freeze.Reason != "plan contradicts the auth model" {
		t.Errorf("freeze.Reason = %q", freeze.Reason)
	}
	if freeze.TriggeringIssueID != "42" {
		t.Errorf("freeze.TriggeringIssueID = %q, want 42", freeze.TriggeringIssueID)
	}
	if freeze.CreatedAt.IsZero() {
		t.Error("freeze.CreatedAt is zero")
	}

	// Freezing again is idempotent: it refreshes rather than erroring.
	if err := store.FreezeFeature(ctx, "feature-1", "second escalation", "43"); err != nil {
		t.Fatalf("second FreezeFeature: %v", err)
	}
	_, freeze, err = store.IsFeatureFrozen(ctx, "feature-1")
	if err != nil {
		t.Fatalf("IsFeatureFrozen: %v", err)
	}
	if freeze.Reason != "second escalation" || freeze.TriggeringIssueID != "43" {
		t.Errorf("re-freeze did not refresh: %+v", freeze)
	}

	// Other Features are unaffected.
	frozen, _, err = store.IsFeatureFrozen(ctx, "feature-2")
	if err != nil {
		t.Fatalf("IsFeatureFrozen: %v", err)
	}
	if frozen {
		t.Error("freezing feature-1 must not freeze feature-2")
	}

	if err := store.UnfreezeFeature(ctx, "feature-1"); err != nil {
		t.Fatalf("UnfreezeFeature: %v", err)
	}
	frozen, _, err = store.IsFeatureFrozen(ctx, "feature-1")
	if err != nil {
		t.Fatalf("IsFeatureFrozen: %v", err)
	}
	if frozen {
		t.Error("UnfreezeFeature did not clear the freeze")
	}

	// Unfreezing an unfrozen Feature is a no-op.
	if err := store.UnfreezeFeature(ctx, "feature-1"); err != nil {
		t.Fatalf("repeat UnfreezeFeature: %v", err)
	}
}

// TestFreezeIsIndependentOfPlanningLease pins the ordering invariant the
// replan handler relies on: a freeze can be written for a Feature that has
// no planning lease and no Planning Execution at all, so freeze can strictly
// precede lease acquisition.
func TestFreezeIsIndependentOfPlanningLease(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	if err := store.FreezeFeature(ctx, "unplanned-feature", "reason", "1"); err != nil {
		t.Fatalf("FreezeFeature with no planning execution: %v", err)
	}
	if _, err := store.FeaturePlanningLease(ctx, "unplanned-feature"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("FeaturePlanningLease err = %v, want ErrNotFound", err)
	}
	frozen, _, err := store.IsFeatureFrozen(ctx, "unplanned-feature")
	if err != nil {
		t.Fatalf("IsFeatureFrozen: %v", err)
	}
	if !frozen {
		t.Fatal("freeze must be durable without any planning lease")
	}
}

func TestReplanCheckpointRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	if _, err := store.GetReplanCheckpoint(ctx, "exec-1", "7"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("GetReplanCheckpoint err = %v, want ErrNotFound", err)
	}

	created := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	want := storage.ReplanCheckpoint{
		ExecutionID:          "exec-1",
		IssueID:              "7",
		FeatureID:            "feature-1",
		Reason:               "the plan assumes a synchronous API that does not exist",
		Evidence:             "internal/api/client.go exposes only a streaming interface",
		AffectedRequirements: []string{"REQ-3", "REQ-4"},
		SuggestedQuestion:    "should the feature adopt the streaming interface?",
		PlanRevision:         "plan-rev-1",
		Frozen:               true,
		CreatedAt:            created,
	}
	if err := store.SaveReplanCheckpoint(ctx, want); err != nil {
		t.Fatalf("SaveReplanCheckpoint: %v", err)
	}

	got, err := store.GetReplanCheckpoint(ctx, "exec-1", "7")
	if err != nil {
		t.Fatalf("GetReplanCheckpoint: %v", err)
	}
	if got.Reason != want.Reason || got.Evidence != want.Evidence {
		t.Errorf("reason/evidence did not round-trip: %+v", got)
	}
	if len(got.AffectedRequirements) != 2 || got.AffectedRequirements[0] != "REQ-3" || got.AffectedRequirements[1] != "REQ-4" {
		t.Errorf("AffectedRequirements = %v", got.AffectedRequirements)
	}
	if got.SuggestedQuestion != want.SuggestedQuestion || got.PlanRevision != "plan-rev-1" {
		t.Errorf("question/plan revision did not round-trip: %+v", got)
	}
	if !got.Frozen {
		t.Error("Frozen did not round-trip")
	}
	if got.LeaseExecutionID != "" || got.DecisionID != "" {
		t.Errorf("unset side-effect fields should stay empty: %+v", got)
	}
	if !got.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, created)
	}

	// Saving again replaces rather than duplicating, and carries the later
	// side effects.
	want.DecisionID = "009-streaming-api"
	want.LeaseExecutionID = "plan-exec-1"
	if err := store.SaveReplanCheckpoint(ctx, want); err != nil {
		t.Fatalf("second SaveReplanCheckpoint: %v", err)
	}
	got, err = store.GetReplanCheckpoint(ctx, "exec-1", "7")
	if err != nil {
		t.Fatalf("GetReplanCheckpoint: %v", err)
	}
	if got.DecisionID != "009-streaming-api" || got.LeaseExecutionID != "plan-exec-1" {
		t.Errorf("update did not persist: %+v", got)
	}
}
