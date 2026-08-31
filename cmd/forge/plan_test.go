package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningagent"
)

// chdirTemp changes the working directory to dir for the duration of the
// test and restores it afterward, mirroring resume_test.go's pattern for
// exercising a run* function in-process against a fixture directory.
func chdirTemp(t *testing.T, dir string) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
}

func writeGoalFixture(t *testing.T, dir, featureID string) {
	t.Helper()
	goal := &planning.Artifact{
		Kind:     planning.KindGoal,
		Sections: []planning.Section{{Heading: "Goal", Body: "Build a widget"}},
	}
	goal.Revision = planning.ComputeRevision(goal)
	featureDir := filepath.Join(dir, ".forge", "features", featureID)
	if err := os.MkdirAll(featureDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(featureDir, "goal.md"), planning.Render(goal), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeApprovedSpecFixture(t *testing.T, dir, featureID string) {
	t.Helper()
	spec := &planning.Artifact{
		Kind: planning.KindSpec,
		Sections: []planning.Section{
			{Heading: "Requirements", Body: "REQ-001: do the thing\n"},
		},
	}
	spec.Revision = planning.ComputeRevision(spec)
	spec.ApprovedRevision = spec.Revision
	spec.State = "approved"
	writeSpecFixture(t, dir, featureID, spec)
}

// TestBuildSpecEngine_CompilesFullRepositoryContext confirms `forge plan`
// grounds the SpecEngine it builds in the repository's real structure
// (ticket 206) rather than a base-revision-only Repository Context: the
// repo-context compiler must actually run against repoRoot, so
// ProjectStructure and Languages are populated from real repository files.
func TestBuildSpecEngine_CompilesFullRepositoryContext(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/widget\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "internal"), 0o755); err != nil {
		t.Fatalf("mkdir internal: %v", err)
	}

	backend := planningagent.NewFakeBackend()
	engine, err := buildSpecEngine(config.Default(), backend, dir, "base-rev", nil)
	if err != nil {
		t.Fatalf("buildSpecEngine: %v", err)
	}

	if engine.Repository.BaseRevision != "base-rev" {
		t.Errorf("Repository.BaseRevision = %q, want base-rev", engine.Repository.BaseRevision)
	}
	if !strings.Contains(engine.Repository.ProjectStructure, "go.mod") {
		t.Errorf("Repository.ProjectStructure = %q, want to contain go.mod", engine.Repository.ProjectStructure)
	}
	if !strings.Contains(engine.Repository.ProjectStructure, "internal/") {
		t.Errorf("Repository.ProjectStructure = %q, want to contain internal/", engine.Repository.ProjectStructure)
	}
	found := false
	for _, lang := range engine.Repository.Languages {
		if lang == "Go" {
			found = true
		}
	}
	if !found {
		t.Errorf("Repository.Languages = %v, want to contain Go", engine.Repository.Languages)
	}
}

// TestRunPlan_NoGoal_StopsCleanly confirms `forge plan` stops with a clean
// exit (not an error) when a Feature has no goal.md yet, rather than
// treating the missing file as a failure.
func TestRunPlan_NoGoal_StopsCleanly(t *testing.T) {
	dir := t.TempDir()
	chdirTemp(t, dir)

	if code := runPlan([]string{"widget"}); code != 0 {
		t.Fatalf("runPlan = %d, want 0", code)
	}
	if _, err := os.Stat(filepath.Join(dir, ".forge", "features", "widget", "spec.md")); !os.IsNotExist(err) {
		t.Fatalf("expected no spec.md to be created, stat err = %v", err)
	}
}

// TestRunPlan_SkipsGenerationWhenArtifactsAlreadyApproved is ticket 21's
// idempotency acceptance criterion end to end: with an approved spec and
// ticket plan already on disk, `forge plan` must not regenerate either and
// must report the pipeline complete.
func TestRunPlan_SkipsGenerationWhenArtifactsAlreadyApproved(t *testing.T) {
	dir := t.TempDir()
	featureID := "widget"
	writeGoalFixture(t, dir, featureID)
	writeApprovedSpecFixture(t, dir, featureID)

	tp := &planning.Artifact{
		Kind:     planning.KindTicketPlan,
		Sections: []planning.Section{{Heading: "Ticket: TKT-001", Body: "### Objective\nDo it.\n"}},
	}
	tp.Revision = planning.ComputeRevision(tp)
	tp.ApprovedRevision = tp.Revision
	tp.State = "approved"
	writeTicketPlanFixture(t, dir, featureID, tp)

	specBefore := readSpecFixture(t, dir, featureID)
	tpBefore := readTicketPlanFixture(t, dir, featureID)

	chdirTemp(t, dir)
	if code := runPlan([]string{featureID}); code != 0 {
		t.Fatalf("runPlan = %d, want 0", code)
	}

	specAfter := readSpecFixture(t, dir, featureID)
	tpAfter := readTicketPlanFixture(t, dir, featureID)
	if specAfter.Revision != specBefore.Revision {
		t.Errorf("spec.md was regenerated: revision changed from %s to %s", specBefore.Revision, specAfter.Revision)
	}
	if tpAfter.Revision != tpBefore.Revision {
		t.Errorf("ticket-plan.md was regenerated: revision changed from %s to %s", tpBefore.Revision, tpAfter.Revision)
	}
}

// TestRunPlan_StopsAtSpecApprovalGate confirms an existing, unapproved
// spec.md blocks `forge plan` from ever generating a ticket plan -- the
// human gate criterion.
func TestRunPlan_StopsAtSpecApprovalGate(t *testing.T) {
	dir := t.TempDir()
	featureID := "widget"
	writeGoalFixture(t, dir, featureID)

	spec := &planning.Artifact{
		Kind:     planning.KindSpec,
		Sections: []planning.Section{{Heading: "Requirements", Body: "REQ-001: do it\n"}},
	}
	spec.Revision = planning.ComputeRevision(spec)
	writeSpecFixture(t, dir, featureID, spec)

	chdirTemp(t, dir)
	if code := runPlan([]string{featureID}); code != 0 {
		t.Fatalf("runPlan = %d, want 0", code)
	}
	if _, err := os.Stat(filepath.Join(dir, ".forge", "features", featureID, "ticket-plan.md")); !os.IsNotExist(err) {
		t.Fatalf("expected no ticket-plan.md while spec is unapproved, stat err = %v", err)
	}
}

// TestRunPlan_UntilSpec_StopsEvenWhenSpecApproved confirms --until spec
// bounds the run to the spec stage even when the spec is already approved
// and a ticket plan could otherwise be generated.
func TestRunPlan_UntilSpec_StopsEvenWhenSpecApproved(t *testing.T) {
	dir := t.TempDir()
	featureID := "widget"
	writeGoalFixture(t, dir, featureID)
	writeApprovedSpecFixture(t, dir, featureID)

	chdirTemp(t, dir)
	if code := runPlan([]string{featureID, "--until", "spec"}); code != 0 {
		t.Fatalf("runPlan = %d, want 0", code)
	}
	if _, err := os.Stat(filepath.Join(dir, ".forge", "features", featureID, "ticket-plan.md")); !os.IsNotExist(err) {
		t.Fatalf("expected --until spec to stop before generating a ticket plan, stat err = %v", err)
	}
}

// TestRunPlan_StopsAtTicketApprovalGate confirms an existing, unapproved
// ticket-plan.md is reported as awaiting approval rather than being
// regenerated or silently passed through.
func TestRunPlan_StopsAtTicketApprovalGate(t *testing.T) {
	dir := t.TempDir()
	featureID := "widget"
	writeGoalFixture(t, dir, featureID)
	writeApprovedSpecFixture(t, dir, featureID)

	tp := &planning.Artifact{
		Kind:     planning.KindTicketPlan,
		Sections: []planning.Section{{Heading: "Ticket: TKT-001", Body: "### Objective\nDo it.\n"}},
	}
	tp.Revision = planning.ComputeRevision(tp)
	writeTicketPlanFixture(t, dir, featureID, tp)
	tpBefore := readTicketPlanFixture(t, dir, featureID)

	chdirTemp(t, dir)
	if code := runPlan([]string{featureID}); code != 0 {
		t.Fatalf("runPlan = %d, want 0", code)
	}

	tpAfter := readTicketPlanFixture(t, dir, featureID)
	if tpAfter.Revision != tpBefore.Revision {
		t.Errorf("ticket-plan.md was regenerated while unapproved: revision changed from %s to %s", tpBefore.Revision, tpAfter.Revision)
	}
	if planning.Approved(tpAfter) {
		t.Errorf("ticket plan should remain unapproved")
	}
}

// TestRunPlan_InvalidUntilValue_Errors confirms an unrecognized --until
// value is rejected before any I/O.
func TestRunPlan_InvalidUntilValue_Errors(t *testing.T) {
	if code := runPlan([]string{"widget", "--until", "bogus"}); code != 1 {
		t.Fatalf("runPlan with invalid --until = %d, want 1", code)
	}
}

// TestRunPlan_MissingFeatureID_Errors confirms --until without a
// feature-id is rejected.
func TestRunPlan_MissingFeatureID_Errors(t *testing.T) {
	if code := runPlan([]string{"--until", "spec"}); code != 1 {
		t.Fatalf("runPlan with no feature-id = %d, want 1", code)
	}
}

// TestRunPlan_PausedPlanningExecution_StopsAtNeedsHumanGate seeds a
// Planning Execution already paused on NEEDS_HUMAN (as if a prior `forge
// plan` run paused on a Decision, then this process restarted) and
// confirms `forge plan` reports the gate and performs no further work,
// rather than generating a spec out from under an unresolved Decision.
func TestRunPlan_PausedPlanningExecution_StopsAtNeedsHumanGate(t *testing.T) {
	repoRoot, base := newTempRepo(t)
	runGit(t, repoRoot, "remote", "add", "origin", "git@github.com:acme/widgets.git")

	cfgPath := filepath.Join(repoRoot, ".forge.yaml")
	yaml := "version: 1\ngit:\n  base: main\ntracker:\n  skip_auth_preflight: true\n"
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	featureID := "widget"
	writeGoalFixture(t, repoRoot, featureID)

	dbPath := filepath.Join(repoRoot, ".forge", "forge.db")
	ctx := context.Background()
	store, err := openStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	exec := domain.PlanningExecution{
		ID:           "plan-exec-1",
		FeatureID:    featureID,
		BaseRevision: base,
		Status:       domain.PlanningStatusNeedsHuman,
		StartedAt:    time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC),
	}
	if err := store.CreatePlanningExecution(ctx, exec); err != nil {
		t.Fatalf("CreatePlanningExecution: %v", err)
	}
	if err := store.ClaimFeaturePlanningLease(ctx, featureID, exec.ID); err != nil {
		t.Fatalf("ClaimFeaturePlanningLease: %v", err)
	}
	// A very high, essentially-guaranteed-dead PID: forge treats an
	// abandoned lease (owner process no longer running) as reclaimable
	// rather than a live conflict, the same as a process restart.
	if err := store.UpdatePlanningLeaseOwner(ctx, featureID, 2000000000); err != nil {
		t.Fatalf("UpdatePlanningLeaseOwner: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	chdirTemp(t, repoRoot)
	if code := runPlan([]string{featureID}); code != 0 {
		t.Fatalf("runPlan = %d, want 0", code)
	}

	if _, err := os.Stat(filepath.Join(repoRoot, ".forge", "features", featureID, "spec.md")); !os.IsNotExist(err) {
		t.Fatalf("expected no spec.md while paused on needs-human, stat err = %v", err)
	}

	reopened, err := openStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer func() { _ = reopened.Close() }()
	reloaded, err := reopened.LoadPlanningExecution(ctx, exec.ID)
	if err != nil {
		t.Fatalf("LoadPlanningExecution: %v", err)
	}
	if reloaded.Status != domain.PlanningStatusNeedsHuman {
		t.Errorf("planning execution status = %s, want NEEDS_HUMAN (untouched)", reloaded.Status)
	}
}
