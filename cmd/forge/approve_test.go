package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
)

// TestApproveTickets_BindsToRevisionAndInvalidatesOnEdit exercises `forge
// approve <feature-id> tickets` end to end: it must approve the ticket
// plan at its current content revision, and a subsequent hand-edit of the
// plan's definitional content must invalidate that approval (ticket 19's
// third acceptance criterion).
func TestApproveTickets_BindsToRevisionAndInvalidatesOnEdit(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	featureID := "widget"

	tpArtifact := &planning.Artifact{
		Kind: planning.KindTicketPlan,
		DerivedFrom: []planning.DerivedFromEntry{
			{Kind: planning.KindSpec, ID: "spec", Revision: "spec-rev"},
		},
		Sections: []planning.Section{
			{Heading: "Ticket: TKT-001", Body: "### Objective\nDo the thing.\n"},
		},
	}
	tpArtifact.Revision = planning.ComputeRevision(tpArtifact)
	writeTicketPlanFixture(t, dir, featureID, tpArtifact)

	cmd := exec.Command(bin, "approve", featureID, "tickets")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("forge approve %s tickets failed: %v\n%s", featureID, err, out)
	}
	if !strings.Contains(string(out), "ticket-plan.md approved for feature "+featureID) {
		t.Errorf("unexpected output: %s", out)
	}

	approved := readTicketPlanFixture(t, dir, featureID)
	if approved.ApprovedRevision == "" {
		t.Fatalf("expected ApprovedRevision to be set, got empty")
	}
	if !planning.Approved(approved) {
		t.Fatalf("expected ticket plan to be Approved() after `forge approve tickets`")
	}
	if planning.Stale(approved) {
		t.Fatalf("freshly approved ticket plan should not be Stale()")
	}

	// Hand-edit the plan's definitional content (a Section body) without
	// updating Revision, mirroring a human editing ticket-plan.md directly.
	approved.Sections[0].Body = "### Objective\nDo a different thing.\n"
	writeTicketPlanFixtureRaw(t, dir, featureID, planning.Render(approved))

	edited := readTicketPlanFixture(t, dir, featureID)
	if planning.Approved(edited) {
		t.Fatalf("editing ticket plan content should invalidate approval, but Approved() is still true")
	}
	if !planning.Stale(edited) {
		t.Fatalf("hand-edited ticket plan should be Stale()")
	}
}

// TestApprove_DispatchesSpecAndTicketsIndependently confirms `forge
// approve <feature-id> spec` and `forge approve <feature-id> tickets`
// route to their respective handlers rather than one shadowing the other.
func TestApprove_DispatchesSpecAndTicketsIndependently(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	featureID := "widget"

	specArtifact := &planning.Artifact{
		Kind: planning.KindSpec,
		Sections: []planning.Section{
			{Heading: "Requirements", Body: "REQ-001: do the thing\n"},
		},
	}
	specArtifact.Revision = planning.ComputeRevision(specArtifact)
	writeSpecFixture(t, dir, featureID, specArtifact)

	tpArtifact := &planning.Artifact{
		Kind: planning.KindTicketPlan,
		Sections: []planning.Section{
			{Heading: "Ticket: TKT-001", Body: "### Objective\nDo the thing.\n"},
		},
	}
	tpArtifact.Revision = planning.ComputeRevision(tpArtifact)
	writeTicketPlanFixture(t, dir, featureID, tpArtifact)

	specCmd := exec.Command(bin, "approve", featureID, "spec")
	specCmd.Dir = dir
	specOut, err := specCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("forge approve %s spec failed: %v\n%s", featureID, err, specOut)
	}
	if !strings.Contains(string(specOut), "spec.md approved") {
		t.Errorf("expected spec approval output, got: %s", specOut)
	}

	ticketsCmd := exec.Command(bin, "approve", featureID, "tickets")
	ticketsCmd.Dir = dir
	ticketsOut, err := ticketsCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("forge approve %s tickets failed: %v\n%s", featureID, err, ticketsOut)
	}
	if !strings.Contains(string(ticketsOut), "ticket-plan.md approved") {
		t.Errorf("expected ticket plan approval output, got: %s", ticketsOut)
	}

	approvedSpec := readSpecFixture(t, dir, featureID)
	if !planning.Approved(approvedSpec) {
		t.Fatalf("expected spec to remain Approved() after approving tickets too")
	}
	approvedTP := readTicketPlanFixture(t, dir, featureID)
	if !planning.Approved(approvedTP) {
		t.Fatalf("expected ticket plan to be Approved()")
	}
}

func writeSpecFixture(t *testing.T, dir, featureID string, a *planning.Artifact) {
	t.Helper()
	featureDir := filepath.Join(dir, ".forge", "features", featureID)
	if err := os.MkdirAll(featureDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(featureDir, "spec.md"), planning.Render(a), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readSpecFixture(t *testing.T, dir, featureID string) *planning.Artifact {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".forge", "features", featureID, "spec.md"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := planning.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func writeTicketPlanFixture(t *testing.T, dir, featureID string, a *planning.Artifact) {
	t.Helper()
	writeTicketPlanFixtureRaw(t, dir, featureID, planning.Render(a))
}

func writeTicketPlanFixtureRaw(t *testing.T, dir, featureID string, data []byte) {
	t.Helper()
	featureDir := filepath.Join(dir, ".forge", "features", featureID)
	if err := os.MkdirAll(featureDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(featureDir, "ticket-plan.md"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readTicketPlanFixture(t *testing.T, dir, featureID string) *planning.Artifact {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".forge", "features", featureID, "ticket-plan.md"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := planning.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// TestApproveTickets_SupersedesDroppedIssuesAndUnfreezesFeature is ticket
// 22's acceptance item 5 end to end through the CLI: approving a new Ticket
// Plan for a frozen Feature closes the unstarted Issues that plan no longer
// contains (as CANCELLED, with an issue.superseded Event) and only then
// lifts the freeze, so frozen work cannot resume before a new plan exists.
func TestApproveTickets_SupersedesDroppedIssuesAndUnfreezesFeature(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	featureID := "widget"
	ctx := context.Background()

	if err := os.MkdirAll(filepath.Join(dir, ".forge"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(filepath.Join(dir, ".forge", "forge.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := store.CreateExecution(ctx, domain.Execution{ID: "exec-1", BaseRevision: "base"}); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}

	body := func(tempKey string) string {
		return "### Objective\ndo it\n\n" + tracker.RenderForgeProvenance(tracker.ForgeProvenance{
			Status:       tracker.ProvenanceReady,
			TempKey:      tempKey,
			Project:      featureID,
			SpecRevision: "spec-rev",
			PlanRevision: "plan-rev-1",
		})
	}
	for _, seed := range []struct{ id, tempKey string }{{"1", "TKT-001"}, {"2", "TKT-002"}} {
		if err := store.CreateIssue(ctx, domain.Issue{
			ExecutionID: "exec-1",
			ID:          seed.id,
			Body:        body(seed.tempKey),
			State:       domain.StatePending,
			Scope:       domain.ScopeManaged,
			RetryBudget: domain.NewRetryBudget(config.Default().Retry),
		}); err != nil {
			t.Fatalf("CreateIssue %s: %v", seed.id, err)
		}
	}
	if err := store.FreezeFeature(ctx, featureID, "plan invalidated", "1"); err != nil {
		t.Fatalf("FreezeFeature: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The new plan keeps TKT-001 and drops TKT-002.
	tpArtifact := &planning.Artifact{
		Kind: planning.KindTicketPlan,
		Sections: []planning.Section{{
			Heading: "Ticket: TKT-001",
			Body: "### Objective\nDo the thing.\n\n### Requirements\nREQ-001: do it\n\n" +
				"### Acceptance Criteria\n- it is done\n",
		}},
	}
	tpArtifact.Revision = planning.ComputeRevision(tpArtifact)
	writeTicketPlanFixture(t, dir, featureID, tpArtifact)

	cmd := exec.Command(bin, "approve", featureID, "tickets")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("forge approve %s tickets failed: %v\n%s", featureID, err, out)
	}
	if !strings.Contains(string(out), "issue 2 closed as superseded") {
		t.Errorf("expected issue 2 to be reported superseded, got: %s", out)
	}
	if !strings.Contains(string(out), "feature "+featureID+" unfrozen") {
		t.Errorf("expected the feature to be reported unfrozen, got: %s", out)
	}

	store, err = storage.Open(filepath.Join(dir, ".forge", "forge.db"))
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer func() { _ = store.Close() }()

	kept, err := store.GetIssue(ctx, "exec-1", "1")
	if err != nil {
		t.Fatalf("GetIssue 1: %v", err)
	}
	if kept.State != domain.StatePending {
		t.Errorf("issue 1 state = %s, want PENDING (still in the new plan)", kept.State)
	}
	dropped, err := store.GetIssue(ctx, "exec-1", "2")
	if err != nil {
		t.Fatalf("GetIssue 2: %v", err)
	}
	if dropped.State != domain.StateCancelled {
		t.Errorf("issue 2 state = %s, want CANCELLED (superseded, not recycled)", dropped.State)
	}

	events, err := store.EventsByIssue(ctx, "exec-1", "2")
	if err != nil {
		t.Fatalf("EventsByIssue: %v", err)
	}
	var sawSuperseded bool
	for _, e := range events {
		if e.Type == "issue.superseded" {
			sawSuperseded = true
		}
	}
	if !sawSuperseded {
		t.Error("no issue.superseded event was recorded")
	}

	frozen, _, err := store.IsFeatureFrozen(ctx, featureID)
	if err != nil {
		t.Fatalf("IsFeatureFrozen: %v", err)
	}
	if frozen {
		t.Error("approving a new ticket plan did not unfreeze the feature")
	}
}
