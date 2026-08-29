package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/planning"
)

// writeResolvedDecisionFixture writes a single resolved-and-approved
// Decision Artifact for featureID, the minimum a Feature needs for
// planning.DeriveStage to move past StageDecisions.
func writeResolvedDecisionFixture(t *testing.T, dir, featureID, id string) {
	t.Helper()
	decision := &planning.Artifact{
		Kind:     planning.KindDecision,
		State:    "resolved",
		Sections: []planning.Section{{Heading: "Question", Body: "Which storage?"}, {Heading: "Outcome", Body: "SQLite"}},
	}
	decision.Revision = planning.ComputeRevision(decision)
	decision.ApprovedRevision = decision.Revision
	decisionsDir := filepath.Join(dir, ".forge", "features", featureID, "decisions")
	if err := os.MkdirAll(decisionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(decisionsDir, id+".md"), planning.Render(decision), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRunStatus_RoutesFeatureIDToFeatureStatus confirms `forge status
// <feature-id>` (a directory under .forge/features, as opposed to an
// Execution UUID) is routed to the feature-scoped report rather than
// erroring as an unknown Execution.
func TestRunStatus_RoutesFeatureIDToFeatureStatus(t *testing.T) {
	dir := t.TempDir()
	featureID := "widget"
	writeGoalFixture(t, dir, featureID)
	chdirTemp(t, dir)

	if code := runStatus([]string{featureID}); code != 0 {
		t.Fatalf("runStatus(%q) = %d, want 0", featureID, code)
	}
}

// TestLoadFeatureStatus_GoalOnly confirms a Feature with only a goal.md
// reports StageDecisions and directs the operator to run `forge plan`.
func TestLoadFeatureStatus_GoalOnly(t *testing.T) {
	dir := t.TempDir()
	featureID := "widget"
	writeGoalFixture(t, dir, featureID)
	chdirTemp(t, dir)

	ctx := context.Background()
	store, err := openStore(ctx, filepath.Join(dir, ".forge", "forge.db"))
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	report, err := loadFeatureStatus(ctx, store, featureID)
	if err != nil {
		t.Fatalf("loadFeatureStatus: %v", err)
	}
	if report.Stage != planning.StageDecisions {
		t.Errorf("stage = %s, want %s", report.Stage, planning.StageDecisions)
	}
	if !strings.Contains(report.NextAction, "forge plan") {
		t.Errorf("next action = %q, want it to mention forge plan", report.NextAction)
	}
}

// TestLoadFeatureStatus_UnapprovedSpec_ReportsApprovalGate confirms an
// existing but unapproved spec is surfaced as not approved and the next
// action points at `forge approve`.
func TestLoadFeatureStatus_UnapprovedSpec_ReportsApprovalGate(t *testing.T) {
	dir := t.TempDir()
	featureID := "widget"
	writeGoalFixture(t, dir, featureID)
	writeResolvedDecisionFixture(t, dir, featureID, "001-storage")

	spec := &planning.Artifact{
		Kind:     planning.KindSpec,
		Sections: []planning.Section{{Heading: "Requirements", Body: "REQ-001: do it\n"}},
	}
	spec.Revision = planning.ComputeRevision(spec)
	writeSpecFixture(t, dir, featureID, spec)
	chdirTemp(t, dir)

	ctx := context.Background()
	store, err := openStore(ctx, filepath.Join(dir, ".forge", "forge.db"))
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	report, err := loadFeatureStatus(ctx, store, featureID)
	if err != nil {
		t.Fatalf("loadFeatureStatus: %v", err)
	}
	if report.Stage != planning.StageTicketPlan {
		t.Errorf("stage = %s, want %s", report.Stage, planning.StageTicketPlan)
	}
	if !report.SpecExists || report.SpecApproved {
		t.Errorf("spec exists=%t approved=%t, want exists=true approved=false", report.SpecExists, report.SpecApproved)
	}
	if !strings.Contains(report.NextAction, "forge approve widget spec") {
		t.Errorf("next action = %q, want it to mention forge approve widget spec", report.NextAction)
	}
}

// TestLoadFeatureStatus_AllApproved_NextActionIsMaterialize confirms a
// fully approved plan reports StageDone and points at `forge materialize`.
func TestLoadFeatureStatus_AllApproved_NextActionIsMaterialize(t *testing.T) {
	dir := t.TempDir()
	featureID := "widget"
	writeGoalFixture(t, dir, featureID)
	writeResolvedDecisionFixture(t, dir, featureID, "001-storage")
	writeApprovedSpecFixture(t, dir, featureID)

	tp := &planning.Artifact{
		Kind:     planning.KindTicketPlan,
		Sections: []planning.Section{{Heading: "Ticket: TKT-001", Body: "### Objective\nDo it.\n"}},
	}
	tp.Revision = planning.ComputeRevision(tp)
	tp.ApprovedRevision = tp.Revision
	writeTicketPlanFixture(t, dir, featureID, tp)
	chdirTemp(t, dir)

	ctx := context.Background()
	store, err := openStore(ctx, filepath.Join(dir, ".forge", "forge.db"))
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	report, err := loadFeatureStatus(ctx, store, featureID)
	if err != nil {
		t.Fatalf("loadFeatureStatus: %v", err)
	}
	if report.Stage != planning.StageDone {
		t.Errorf("stage = %s, want %s", report.Stage, planning.StageDone)
	}
	if !report.TicketPlanApproved {
		t.Error("expected ticket plan to be reported approved")
	}
	if !strings.Contains(report.NextAction, "forge materialize widget") {
		t.Errorf("next action = %q, want it to mention forge materialize widget", report.NextAction)
	}
}

// TestLoadFeatureStatus_StaleSpec_ReportedAsStaleArtifact confirms a spec
// whose derived_from goal revision no longer matches the goal's current
// revision (the goal was hand-edited after the spec was generated) is
// surfaced in StaleArtifacts.
func TestLoadFeatureStatus_StaleSpec_ReportedAsStaleArtifact(t *testing.T) {
	dir := t.TempDir()
	featureID := "widget"

	goal := &planning.Artifact{Kind: planning.KindGoal, Sections: []planning.Section{{Heading: "Goal", Body: "Build a widget"}}}
	goal.Revision = planning.ComputeRevision(goal)
	goalDir := filepath.Join(dir, ".forge", "features", featureID)
	if err := os.MkdirAll(goalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), planning.Render(goal), 0o644); err != nil {
		t.Fatal(err)
	}

	spec := &planning.Artifact{
		Kind:     planning.KindSpec,
		Sections: []planning.Section{{Heading: "Requirements", Body: "REQ-001: do it\n"}},
		DerivedFrom: []planning.DerivedFromEntry{
			{Kind: planning.KindGoal, ID: "goal", Revision: "stale-goal-revision"},
		},
	}
	spec.Revision = planning.ComputeRevision(spec)
	spec.ApprovedRevision = spec.Revision
	writeSpecFixture(t, dir, featureID, spec)
	chdirTemp(t, dir)

	ctx := context.Background()
	store, err := openStore(ctx, filepath.Join(dir, ".forge", "forge.db"))
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	report, err := loadFeatureStatus(ctx, store, featureID)
	if err != nil {
		t.Fatalf("loadFeatureStatus: %v", err)
	}
	found := false
	for _, s := range report.StaleArtifacts {
		if s == "spec.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("stale artifacts = %v, want spec.md included", report.StaleArtifacts)
	}
}

func TestPrintFeatureStatus_IncludesKeyFields(t *testing.T) {
	report := FeatureStatusReport{
		FeatureID:           "widget",
		Stage:               planning.StageSpec,
		Frontier:            []string{"001-storage"},
		SpecExists:          false,
		TicketPlanExists:    false,
		StaleArtifacts:      []string{"decisions/001-storage.md"},
		PlanningExecutionID: "exec-1",
		PlanningStatus:      "ACTIVE",
		NextAction:          "run `forge plan widget` to continue resolving decisions",
	}
	var buf bytes.Buffer
	printFeatureStatus(&buf, report)
	out := buf.String()
	for _, want := range []string{"widget", "spec", "001-storage", "decisions/001-storage.md", "exec-1", "ACTIVE", "forge plan widget"} {
		if !strings.Contains(out, want) {
			t.Errorf("printFeatureStatus output missing %q:\n%s", want, out)
		}
	}
}
