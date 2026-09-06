package main

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/planengine"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningapprove"
	"github.com/Teagan42/forge/internal/repolock"
)

// TestParsePlanArgs_TUIFlags confirms --tui and --no-tui bind the shared
// triState and coexist with the positional feature-id and --until.
func TestParsePlanArgs_TUIFlags(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantSet bool
		wantVal bool
	}{
		{name: "no flag leaves triState unset", args: []string{"widget"}, wantSet: false},
		{name: "--tui forces on", args: []string{"widget", "--tui"}, wantSet: true, wantVal: true},
		{name: "--no-tui forces off", args: []string{"--no-tui", "widget"}, wantSet: true, wantVal: false},
		{name: "--tui with --until", args: []string{"widget", "--until", "spec", "--tui"}, wantSet: true, wantVal: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			featureID, _, tui, code, done := parsePlanArgs(tc.args)
			if done {
				t.Fatalf("parsePlanArgs done=true (code %d), want a parsed result", code)
			}
			if featureID != "widget" {
				t.Errorf("featureID = %q, want widget", featureID)
			}
			val, set := tui.wasSet()
			if set != tc.wantSet {
				t.Errorf("triState set = %v, want %v", set, tc.wantSet)
			}
			if set && val != tc.wantVal {
				t.Errorf("triState val = %v, want %v", val, tc.wantVal)
			}
		})
	}
}

// buildDriverFixture starts a Planning Execution over a fixture repo and
// returns the pieces drivePlanPipeline needs. baseRevision and backend are
// left empty/nil on purpose: every scenario here leaves wayfinding skipped and
// every artifact present, so the pipeline never generates and never reaches a
// backend or tracker call.
func buildDriverFixture(t *testing.T, repoRoot, featureID string) (planTUIRequest, *repolock.Locker) {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(repoRoot, ".forge", "forge.db")
	store, err := openStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	planRuntime := planengine.New(store)
	exec, err := planRuntime.Start(ctx, featureID, "base-rev")
	if err != nil {
		t.Fatalf("Start planning execution: %v", err)
	}

	return planTUIRequest{
		Store:       store,
		PlanRuntime: planRuntime,
		RepoRoot:    repoRoot,
		FeatureID:   featureID,
		UntilStage:  "tickets",
		ExecutionID: exec.ID,
		Loader:      &fileArtifactLoader{RepoRoot: repoRoot},
	}, repolock.New(repoRoot)
}

// TestDrivePlanPipeline_CompletesWhenArtifactsApproved confirms the driver's
// happy path: with every artifact already approved, one pipeline pass reaches
// Complete and the driver marks the Planning Execution COMPLETE -- no gate,
// no wait.
func TestDrivePlanPipeline_CompletesWhenArtifactsApproved(t *testing.T) {
	repoRoot := planFixtureRepo(t)
	featureID := "widget"
	writeGoalFixture(t, repoRoot, featureID)
	writeApprovedSpecFixture(t, repoRoot, featureID)

	tp := &planning.Artifact{
		Kind:     planning.KindTicketPlan,
		Sections: []planning.Section{{Heading: "Ticket: TKT-001", Body: "### Objective\nDo it.\n"}},
	}
	tp.Revision = planning.ComputeRevision(tp)
	tp.ApprovedRevision = tp.Revision
	tp.State = "approved"
	writeTicketPlanFixture(t, repoRoot, featureID, tp)

	req, locks := buildDriverFixture(t, repoRoot, featureID)

	stop, err := drivePlanPipeline(context.Background(), req, nil, locks)
	if err != nil {
		t.Fatalf("drivePlanPipeline: %v", err)
	}
	if stop.Reason != reasonComplete {
		t.Fatalf("stop.Reason = %v, want reasonComplete", stop.Reason)
	}

	exec, err := req.Store.LoadPlanningExecution(context.Background(), req.ExecutionID)
	if err != nil {
		t.Fatalf("LoadPlanningExecution: %v", err)
	}
	if exec.Status != domain.PlanningStatusComplete {
		t.Errorf("execution status = %s, want COMPLETE", exec.Status)
	}
}

// TestDrivePlanPipeline_ApprovalGateAdvances confirms the driver blocks at the
// spec approval gate, then continues and completes once the artifact approval
// marker appears -- the store/file coordination the TUI's Approver drives. A
// background goroutine plays the human, approving through the same
// planningapprove.Approver the TUI uses.
func TestDrivePlanPipeline_ApprovalGateAdvances(t *testing.T) {
	repoRoot := planFixtureRepo(t)
	featureID := "widget"
	writeGoalFixture(t, repoRoot, featureID)

	// An unapproved (legacy) spec: the first pass stops at its approval gate.
	spec := &planning.Artifact{
		Kind:     planning.KindSpec,
		Sections: []planning.Section{{Heading: "Requirements", Body: "REQ-001: do it\n"}},
	}
	spec.Revision = planning.ComputeRevision(spec)
	writeSpecFixture(t, repoRoot, featureID, spec)

	// A pre-approved ticket plan, so the second pass completes with no backend.
	tp := &planning.Artifact{
		Kind:     planning.KindTicketPlan,
		Sections: []planning.Section{{Heading: "Ticket: TKT-001", Body: "### Objective\nDo it.\n"}},
	}
	tp.Revision = planning.ComputeRevision(tp)
	tp.ApprovedRevision = tp.Revision
	tp.State = "approved"
	writeTicketPlanFixture(t, repoRoot, featureID, tp)

	req, locks := buildDriverFixture(t, repoRoot, featureID)

	// Shrink the gate poll so the test does not wait whole seconds.
	prev := planGateWait
	planGateWait = 20 * time.Millisecond
	t.Cleanup(func() { planGateWait = prev })

	// Background "human": approve the pending spec once the gate is open.
	approver := &planningapprove.Approver{Store: req.Store, Artifacts: req.Loader, Locks: locks}
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if err := approver.ApprovePlanningArtifact(context.Background(), featureID); err == nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stop, err := drivePlanPipeline(ctx, req, nil, locks)
	if err != nil {
		t.Fatalf("drivePlanPipeline: %v", err)
	}
	if stop.Reason != reasonComplete {
		t.Fatalf("stop.Reason = %v, want reasonComplete", stop.Reason)
	}

	exec, err := req.Store.LoadPlanningExecution(context.Background(), req.ExecutionID)
	if err != nil {
		t.Fatalf("LoadPlanningExecution: %v", err)
	}
	if exec.Status != domain.PlanningStatusComplete {
		t.Errorf("execution status = %s, want COMPLETE", exec.Status)
	}
}

// TestPrintPlanTUIOutcome confirms each stop reason prints the guidance a
// human needs, matching the headless resume/approve/materialize hints.
func TestPrintPlanTUIOutcome(t *testing.T) {
	req := planTUIRequest{FeatureID: "widget", ExecutionID: "exec-1", UntilStage: "spec"}
	cases := []struct {
		reason planStopReason
		want   string
	}{
		{reasonComplete, "forge materialize widget"},
		{reasonUntilReached, "--until spec"},
		{reasonNeedsHuman, "forge resume exec-1"},
		{reasonNeedsApproval, "forge approve widget"},
	}
	for _, tc := range cases {
		var buf bytes.Buffer
		printPlanTUIOutcome(&buf, req, planStop{Reason: tc.reason})
		if !bytes.Contains(buf.Bytes(), []byte(tc.want)) {
			t.Errorf("reason %v: output %q does not contain %q", tc.reason, buf.String(), tc.want)
		}
	}
}
