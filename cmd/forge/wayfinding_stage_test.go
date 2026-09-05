package main

import (
	"context"
	"testing"

	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/gittest"
	"github.com/Teagan42/forge/internal/planengine"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningagent"
	"github.com/Teagan42/forge/internal/tracker"
)

// TestRunWayfindingStage_ResolvesDecisionAndLeavesExecutionActive drives
// runWayfindingStage directly (not through the built binary): a single
// open Decision resolves and the readiness review reports READY_FOR_SPEC.
// runWayfindingStage no longer owns Start/Finish (issue #470 -- the whole
// `forge plan` pipeline shares one Planning Execution), so completing
// wayfinding must leave the execution ACTIVE with its lease still held,
// not COMPLETE: the pipeline still has spec/ticket-plan stages to run.
func TestRunWayfindingStage_ResolvesDecisionAndLeavesExecutionActive(t *testing.T) {
	repoRoot, base := gittest.NewTempRepo(t)
	ctx := context.Background()

	store, err := openStore(ctx, repoRoot+"/.forge/forge.db")
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	cfg := config.Default()
	cfg.Git.Base = "main"
	trk := tracker.NewFakeTracker()

	planRuntime := planengine.New(store)
	exec, err := planRuntime.Start(ctx, "widget", base)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

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
	backend.ProgramResult("decision-resolution", `{"outcome":"SQLite"}`)
	backend.ProgramResult("planning-readiness-review", `{"status":"READY_FOR_SPEC","decisions":[]}`)

	paused, err := runWayfindingStage(ctx, store, trk, cfg, backend, repoRoot, "widget", base, exec.ID, goal, decisions, loader)
	if err != nil {
		t.Fatalf("runWayfindingStage: %v", err)
	}
	if paused {
		t.Fatalf("expected wayfinding to complete, got paused (execution %s)", exec.ID)
	}

	reloaded, err := store.LoadPlanningExecution(ctx, exec.ID)
	if err != nil {
		t.Fatalf("LoadPlanningExecution: %v", err)
	}
	if reloaded.Status != domain.PlanningStatusActive {
		t.Errorf("planning execution status = %s, want ACTIVE (wayfinding is only one stage of the pipeline)", reloaded.Status)
	}
	if _, err := store.FeaturePlanningLease(ctx, "widget"); err != nil {
		t.Errorf("expected the planning lease to remain held after wayfinding alone completes: %v", err)
	}
}

// TestRunWayfindingStage_PausesOnNeedsHuman confirms a NEEDS_HUMAN
// resolution pauses the Planning Execution and reports paused=true, using
// a FakeTracker so the assertion also proves no real tracker network call
// is required for the pause path itself (label and comment are recorded
// in-memory).
func TestRunWayfindingStage_PausesOnNeedsHuman(t *testing.T) {
	repoRoot, base := gittest.NewTempRepo(t)
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

	planRuntime := planengine.New(store)
	exec, err := planRuntime.Start(ctx, "widget", base)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

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
	backend.ProgramResult("decision-resolution", `{"needs_human":{"question":"Which vendor?","context":"Both meet requirements."}}`)
	backend.ProgramResult("planning-readiness-review", `{"status":"READY_FOR_SPEC","decisions":[]}`)

	paused, err := runWayfindingStage(ctx, store, trk, cfg, backend, repoRoot, "widget", base, exec.ID, goal, decisions, loader)
	if err != nil {
		t.Fatalf("runWayfindingStage: %v", err)
	}
	if !paused {
		t.Fatalf("expected wayfinding to pause on needs-human")
	}

	reloaded, err := store.LoadPlanningExecution(ctx, exec.ID)
	if err != nil {
		t.Fatalf("LoadPlanningExecution: %v", err)
	}
	if reloaded.Status != domain.PlanningStatusNeedsHuman {
		t.Errorf("planning execution status = %s, want NEEDS_HUMAN", reloaded.Status)
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
