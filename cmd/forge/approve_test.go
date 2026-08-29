package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/planning"
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
