package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/planning"
)

// TestMaterialize_NoSpec_Errors confirms `forge materialize` fails fast
// (before ever touching a tracker) when the feature has no approved spec.
func TestMaterialize_NoSpec_Errors(t *testing.T) {
	bin := buildBinary(t)
	dir := initGitFixture(t)

	cmd := exec.Command(bin, "materialize", "widget")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected an error, got success: %s", out)
	}
	if !strings.Contains(string(out), "no spec found") {
		t.Errorf("unexpected output: %s", out)
	}
}

// TestMaterialize_UnapprovedSpec_Errors confirms an unapproved spec blocks
// materialization even when a ticket plan exists.
func TestMaterialize_UnapprovedSpec_Errors(t *testing.T) {
	bin := buildBinary(t)
	dir := initGitFixture(t)
	featureID := "widget"

	specArtifact := &planning.Artifact{
		Kind: planning.KindSpec,
		Sections: []planning.Section{
			{Heading: "Requirements", Body: "REQ-001: do the thing\n"},
		},
	}
	specArtifact.Revision = planning.ComputeRevision(specArtifact)
	writeSpecFixture(t, dir, featureID, specArtifact)

	cmd := exec.Command(bin, "materialize", featureID)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected an error, got success: %s", out)
	}
	if !strings.Contains(string(out), "not approved") {
		t.Errorf("unexpected output: %s", out)
	}
}

// TestMaterialize_UnapprovedTicketPlan_Errors confirms an approved spec is
// not sufficient by itself — the ticket plan must also be approved.
func TestMaterialize_UnapprovedTicketPlan_Errors(t *testing.T) {
	bin := buildBinary(t)
	dir := initGitFixture(t)
	featureID := "widget"

	specArtifact := &planning.Artifact{
		Kind: planning.KindSpec,
		Sections: []planning.Section{
			{Heading: "Requirements", Body: "REQ-001: do the thing\n"},
		},
	}
	specArtifact.Revision = planning.ComputeRevision(specArtifact)
	specArtifact.ApprovedRevision = specArtifact.Revision
	writeSpecFixture(t, dir, featureID, specArtifact)

	tpArtifact := &planning.Artifact{
		Kind: planning.KindTicketPlan,
		Sections: []planning.Section{
			{Heading: "Ticket: TKT-001", Body: "### Objective\nDo the thing.\n### Requirements\nREQ-001\n### Acceptance Criteria\n- done\n"},
		},
	}
	tpArtifact.Revision = planning.ComputeRevision(tpArtifact)
	writeTicketPlanFixture(t, dir, featureID, tpArtifact)

	cmd := exec.Command(bin, "materialize", featureID)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected an error, got success: %s", out)
	}
	if !strings.Contains(string(out), "not approved") {
		t.Errorf("unexpected output: %s", out)
	}
}

// TestMaterialize_ApprovedPlan_ReachesTrackerStage confirms both approval
// gates pass and the ticket plan parses, so the command proceeds all the
// way to resolving a real Tracker — which fails here only because the git
// fixture has no 'origin' remote, proving no earlier local check
// incorrectly short-circuited it.
func TestMaterialize_ApprovedPlan_ReachesTrackerStage(t *testing.T) {
	bin := buildBinary(t)
	dir := initGitFixture(t)
	featureID := "widget"

	specArtifact := &planning.Artifact{
		Kind: planning.KindSpec,
		Sections: []planning.Section{
			{Heading: "Requirements", Body: "REQ-001: do the thing\n"},
		},
	}
	specArtifact.Revision = planning.ComputeRevision(specArtifact)
	specArtifact.ApprovedRevision = specArtifact.Revision
	writeSpecFixture(t, dir, featureID, specArtifact)

	tpArtifact := &planning.Artifact{
		Kind: planning.KindTicketPlan,
		DerivedFrom: []planning.DerivedFromEntry{
			{Kind: planning.KindSpec, ID: "spec", Revision: "spec-rev"},
		},
		Sections: []planning.Section{
			{Heading: "Ticket: TKT-001", Body: "### Objective\nDo the thing.\n### Requirements\nREQ-001\n### Acceptance Criteria\n- done\n"},
		},
	}
	tpArtifact.Revision = planning.ComputeRevision(tpArtifact)
	tpArtifact.ApprovedRevision = tpArtifact.Revision
	writeTicketPlanFixture(t, dir, featureID, tpArtifact)

	cmd := exec.Command(bin, "materialize", featureID)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected an error (no 'origin' remote configured), got success: %s", out)
	}
	if !strings.Contains(string(out), "origin") {
		t.Errorf("expected failure to be about the missing 'origin' remote, got: %s", out)
	}
}

// TestMaterialize_FailsLoudlyOutsideGitRepo covers issue #576: `forge
// materialize` must discover the repo root via git (as `forge retry`
// already does for issue #459) instead of silently treating an arbitrary
// cwd as the repo root.
func TestMaterialize_FailsLoudlyOutsideGitRepo(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	cmd := exec.Command(bin, "materialize", "widget")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected an error outside a git repository, got success: %s", out)
	}
	if !strings.Contains(string(out), "not inside a git repository") {
		t.Errorf("unexpected output: %s", out)
	}
}

// TestMaterialize_ResolvesConfigFromSubdirectory covers issue #576: running
// `forge materialize` from a subdirectory must use the repo root's
// .forge.yaml, not silently fall back to config.Default() the way a
// cwd-relative --config default would from any directory but the repo
// root. The fixture's .forge.yaml selects the GitLab tracker, so a repo-
// root-resolved config fails on the GitLab-specific missing-token check;
// falling back to config.Default()'s GitHub tracker would instead fail on
// the missing 'origin' remote (as TestMaterialize_ApprovedPlan_
// ReachesTrackerStage's repo-root run does).
func TestMaterialize_ResolvesConfigFromSubdirectory(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "")

	bin := buildBinary(t)
	dir := initGitFixture(t)
	featureID := "widget"

	if err := os.WriteFile(filepath.Join(dir, ".forge.yaml"), []byte("version: 1\ntracker:\n  type: gitlab\n  gitlab:\n    project: acme/widgets\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	sub := filepath.Join(dir, "sub", "dir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	specArtifact := &planning.Artifact{
		Kind: planning.KindSpec,
		Sections: []planning.Section{
			{Heading: "Requirements", Body: "REQ-001: do the thing\n"},
		},
	}
	specArtifact.Revision = planning.ComputeRevision(specArtifact)
	specArtifact.ApprovedRevision = specArtifact.Revision
	writeSpecFixture(t, dir, featureID, specArtifact)

	tpArtifact := &planning.Artifact{
		Kind: planning.KindTicketPlan,
		DerivedFrom: []planning.DerivedFromEntry{
			{Kind: planning.KindSpec, ID: "spec", Revision: "spec-rev"},
		},
		Sections: []planning.Section{
			{Heading: "Ticket: TKT-001", Body: "### Objective\nDo the thing.\n### Requirements\nREQ-001\n### Acceptance Criteria\n- done\n"},
		},
	}
	tpArtifact.Revision = planning.ComputeRevision(tpArtifact)
	tpArtifact.ApprovedRevision = tpArtifact.Revision
	writeTicketPlanFixture(t, dir, featureID, tpArtifact)

	cmd := exec.Command(bin, "materialize", featureID)
	cmd.Dir = sub
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected an error (missing GITLAB_TOKEN), got success: %s", out)
	}
	if !strings.Contains(string(out), "GITLAB_TOKEN") {
		t.Errorf("expected failure to be about the missing GitLab token (proving the repo root's .forge.yaml was loaded), got: %s", out)
	}
}
