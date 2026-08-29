package main

import (
	"context"
	"testing"

	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/gittest"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningagent"
	"github.com/Teagan42/forge/internal/tracker"
)

// TestRunWayfindingStage_ResolvesDecisionAndCompletesExecution drives
// runWayfindingStage directly (not through the built binary): a single
// open Decision resolves, the readiness review reports READY_FOR_SPEC, and
// the Planning Execution reaches COMPLETE with its lease released so a
// subsequent `forge plan` call would start fresh rather than reclaim it.
func TestRunWayfindingStage_ResolvesDecisionAndCompletesExecution(t *testing.T) {
	repoRoot, _ := gittest.NewTempRepo(t)
	ctx := context.Background()

	store, err := openStore(ctx, repoRoot+"/.forge/forge.db")
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	cfg := config.Default()
	cfg.Git.Base = "main"
	trk := tracker.NewFakeTracker()

	goal := &planning.Artifact{Kind: planning.KindGoal, Sections: []planning.Section{{Heading: "Goal", Body: "Build a widget"}}}
	goal.Revision = planning.ComputeRevision(goal)

	decision := &planning.Artifact{
		Kind:     planning.KindDecision,
		State:    "open",
		Sections: []planning.Section{{Heading: "Question", Body: "Which storage?"}},
	}
	decision.Revision = planning.ComputeRevision(decision)
	decisions := map[string]*planning.Artifact{"001-storage": decision}

	loader := &fileArtifactLoader{featureID: "widget"}

	// runWayfindingStage's Persist callback writes through
	// fileArtifactLoader.SaveDecision, which uses paths relative to the
	// working directory -- mirror runPlan's own cwd-relative convention.
	chdirTemp(t, repoRoot)

	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("decision-resolution", "```json\n"+`{"outcome":"SQLite"}`+"\n```\n")
	backend.ProgramResult("planning-readiness-review", "```json\n"+`{"status":"READY_FOR_SPEC","decisions":[]}`+"\n```\n")

	paused, executionID, err := runWayfindingStage(ctx, store, trk, cfg, backend, repoRoot, "widget", goal, decisions, loader)
	if err != nil {
		t.Fatalf("runWayfindingStage: %v", err)
	}
	if paused {
		t.Fatalf("expected wayfinding to complete, got paused (execution %s)", executionID)
	}

	exec, err := store.LoadPlanningExecution(ctx, executionID)
	if err != nil {
		t.Fatalf("LoadPlanningExecution: %v", err)
	}
	if exec.Status != domain.PlanningStatusComplete {
		t.Errorf("planning execution status = %s, want COMPLETE", exec.Status)
	}
	if _, err := store.FeaturePlanningLease(ctx, "widget"); err == nil {
		t.Errorf("expected the planning lease to be released once the execution completed")
	}
}

// TestRunWayfindingStage_PausesOnNeedsHuman confirms a NEEDS_HUMAN
// resolution pauses the Planning Execution and reports paused=true, using
// a FakeTracker so the assertion also proves no real tracker network call
// is required for the pause path itself (label and comment are recorded
// in-memory).
func TestRunWayfindingStage_PausesOnNeedsHuman(t *testing.T) {
	repoRoot, _ := gittest.NewTempRepo(t)
	ctx := context.Background()

	store, err := openStore(ctx, repoRoot+"/.forge/forge.db")
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	cfg := config.Default()
	cfg.Git.Base = "main"
	trk := tracker.NewFakeTracker()
	trk.AddIssue(domain.Issue{ID: "widget"})

	goal := &planning.Artifact{Kind: planning.KindGoal, Sections: []planning.Section{{Heading: "Goal", Body: "Build a widget"}}}
	goal.Revision = planning.ComputeRevision(goal)

	decision := &planning.Artifact{
		Kind:     planning.KindDecision,
		State:    "open",
		Sections: []planning.Section{{Heading: "Question", Body: "Which vendor?"}},
	}
	decision.Revision = planning.ComputeRevision(decision)
	decisions := map[string]*planning.Artifact{"001-vendor": decision}

	loader := &fileArtifactLoader{featureID: "widget"}
	chdirTemp(t, repoRoot)

	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("decision-resolution", "```json\n"+`{"needs_human":{"question":"Which vendor?","context":"Both meet requirements."}}`+"\n```\n")
	backend.ProgramResult("planning-readiness-review", "```json\n"+`{"status":"READY_FOR_SPEC","decisions":[]}`+"\n```\n")

	paused, executionID, err := runWayfindingStage(ctx, store, trk, cfg, backend, repoRoot, "widget", goal, decisions, loader)
	if err != nil {
		t.Fatalf("runWayfindingStage: %v", err)
	}
	if !paused {
		t.Fatalf("expected wayfinding to pause on needs-human")
	}

	exec, err := store.LoadPlanningExecution(ctx, executionID)
	if err != nil {
		t.Fatalf("LoadPlanningExecution: %v", err)
	}
	if exec.Status != domain.PlanningStatusNeedsHuman {
		t.Errorf("planning execution status = %s, want NEEDS_HUMAN", exec.Status)
	}

	labels := trk.Labels("widget")
	found := false
	for _, l := range labels {
		if l == cfg.Blocked.Label {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the blocked label %q to be applied to the feature's tracker issue, got %v", cfg.Blocked.Label, labels)
	}
}
