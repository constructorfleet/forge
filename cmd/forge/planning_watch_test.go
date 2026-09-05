package main

import (
	"context"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/planningapprove"
	"github.com/Teagan42/forge/internal/storage"
)

func seedPlanningExecution(t *testing.T, store *storage.SQLiteStore, id, featureID string, status domain.PlanningStatus) {
	t.Helper()
	pe := domain.PlanningExecution{ID: id, FeatureID: featureID, BaseRevision: "abc123", Status: status, StartedAt: time.Now()}
	if err := store.CreatePlanningExecution(context.Background(), pe); err != nil {
		t.Fatalf("CreatePlanningExecution: %v", err)
	}
}

func TestBuildPlanningModelWiresApproverAndFeatureID(t *testing.T) {
	store := newWatchTestStore(t)
	seedPlanningExecution(t, store, "plan-1", "feat-1", domain.PlanningStatusNeedsApproval)
	repoRoot := t.TempDir()

	model, err := buildPlanningModel(context.Background(), store, "plan-1", nil, repoRoot)
	if err != nil {
		t.Fatalf("buildPlanningModel: %v", err)
	}
	if model.FeatureID != "feat-1" {
		t.Fatalf("FeatureID = %q, want feat-1", model.FeatureID)
	}

	approver, ok := model.Approver.(*planningapprove.Approver)
	if !ok {
		t.Fatalf("Approver = %T, want *planningapprove.Approver", model.Approver)
	}
	if approver.Store == nil {
		t.Fatal("approver.Store is nil")
	}
	if approver.Artifacts == nil {
		t.Fatal("approver.Artifacts is nil")
	}
	loader, ok := approver.Artifacts.(*fileArtifactLoader)
	if !ok {
		t.Fatalf("approver.Artifacts = %T, want *fileArtifactLoader", approver.Artifacts)
	}
	if loader.RepoRoot != repoRoot {
		t.Fatalf("RepoRoot = %q, want %q", loader.RepoRoot, repoRoot)
	}
}

func TestBuildPlanningModelUnknownExecutionErrors(t *testing.T) {
	store := newWatchTestStore(t)

	if _, err := buildPlanningModel(context.Background(), store, "missing", nil, t.TempDir()); err == nil {
		t.Fatal("buildPlanningModel: want error for an unknown planning execution id, got nil")
	}
}
