package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/decisiongraph"
	"github.com/Teagan42/forge/internal/gittest"
	"github.com/Teagan42/forge/internal/planengine"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningagent"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
	"github.com/Teagan42/forge/internal/wayfinding"
)

// TestRunWayfindingStage_ReResolvesFromHumanAnswer confirms the grill re-entry
// path: a Decision paused for the human, once answered (a resumed checkpoint
// carrying the human's comment), is re-opened and re-resolved from that answer
// rather than staying stuck. The resolver's prompt must carry the human's
// words, and the Decision must land resolved on disk.
func TestRunWayfindingStage_ReResolvesFromHumanAnswer(t *testing.T) {
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

	// A Decision paused on the human (the state the pause path leaves on disk).
	paused := decisiongraph.Pause(&planning.Artifact{
		Kind:     planning.KindDecision,
		Sections: []planning.Section{{Heading: "Question", Body: "Which vendor?"}},
	})
	decisions := map[string]*planning.Artifact{"001-vendor": paused}

	loader := &fileArtifactLoader{}
	chdirTemp(t, repoRoot)

	// A resumed checkpoint carrying the human's answer, exactly the shape
	// ResumeDecision writes.
	resumedAt := time.Now().UTC()
	rc := wayfinding.ResumedDecisionContext{
		PreviousQuestion: "Which vendor?",
		NewComments:      []tracker.Comment{{Author: "alice", Body: "Use vendor A -- we have a contract."}},
	}
	rcJSON, err := json.Marshal(rc)
	if err != nil {
		t.Fatalf("marshal resumed context: %v", err)
	}
	if err := store.SaveDecisionCheckpoint(ctx, storage.DecisionCheckpoint{
		ExecutionID:      exec.ID,
		DecisionID:       "001-vendor",
		DecisionRevision: paused.Revision,
		Question:         "Which vendor?",
		CreatedAt:        resumedAt.Add(-time.Hour),
		ResumedAt:        &resumedAt,
		ResumedContext:   string(rcJSON),
	}); err != nil {
		t.Fatalf("SaveDecisionCheckpoint: %v", err)
	}

	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("decision-resolution", `{"outcome":"Vendor A","rationale":"human chose it","consequences":"none","assumptions":"none"}`)
	backend.ProgramResult("planning-readiness-review", `{"status":"READY_FOR_SPEC","decisions":[]}`)

	pausedAgain, err := runWayfindingStage(ctx, store, trk, cfg, backend, repoRoot, "widget", base, exec.ID, goal, decisions, loader, false)
	if err != nil {
		t.Fatalf("runWayfindingStage: %v", err)
	}
	if pausedAgain {
		t.Fatal("expected the answered Decision to resolve, but wayfinding paused again")
	}

	// The resolver saw the human's answer.
	var resolveInvoked bool
	for _, inv := range backend.Invocations() {
		if strings.Contains(inv.Prompt, "Use vendor A") {
			resolveInvoked = true
		}
	}
	if !resolveInvoked {
		t.Error("the decision-resolution prompt never carried the human's answer")
	}

	// The Decision landed resolved on disk.
	reloaded, err := loader.LoadDecisions(ctx, "widget")
	if err != nil {
		t.Fatalf("LoadDecisions: %v", err)
	}
	d := reloaded["001-vendor"]
	if d == nil {
		t.Fatal("decision 001-vendor missing after re-resolution")
	}
	if !planning.Approved(d) {
		t.Errorf("decision state = %q, want a resolved/approved Decision", d.State)
	}
}
