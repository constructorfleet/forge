package planningapprove_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningapprove"
	"github.com/Teagan42/forge/internal/storage"
)

// fakeArtifacts is an in-memory ArtifactStore double: no filesystem, no
// database, just the two artifact kinds ApprovePlanningArtifact ever reads
// or writes.
type fakeArtifacts struct {
	spec       *planning.Artifact
	ticketPlan *planning.Artifact
}

func (f *fakeArtifacts) LoadSpec(ctx context.Context, featureID string) (*planning.Artifact, error) {
	return f.spec, nil
}

func (f *fakeArtifacts) SaveSpec(ctx context.Context, featureID string, spec *planning.Artifact) error {
	f.spec = spec
	return nil
}

func (f *fakeArtifacts) LoadTicketPlan(ctx context.Context, featureID string) (*planning.Artifact, error) {
	return f.ticketPlan, nil
}

func (f *fakeArtifacts) SaveTicketPlan(ctx context.Context, featureID string, tp *planning.Artifact) error {
	f.ticketPlan = tp
	return nil
}

// fakeStore is an in-memory Store double covering the Planning Execution
// history and replan freeze the Approver reads.
type fakeStore struct {
	executions []domain.PlanningExecution
	frozen     bool
	unfrozen   bool
	closed     []string
}

func (f *fakeStore) ListPlanningExecutionsByFeature(ctx context.Context, featureID string) ([]domain.PlanningExecution, error) {
	return f.executions, nil
}

func (f *fakeStore) IsFeatureFrozen(ctx context.Context, featureID string) (bool, storage.FeatureFreeze, error) {
	return f.frozen, storage.FeatureFreeze{}, nil
}

func (f *fakeStore) UnfreezeFeature(ctx context.Context, featureID string) error {
	f.unfrozen = true
	f.frozen = false
	return nil
}

// ListExecutions, AppendEvent, and TransitionIssue satisfy
// replan.SupersedeStore, exercised through ResumeFeature's supersede pass.
// This test's Features have no in-flight Issues to supersede, so returning
// an empty roster and recording nothing is a faithful double.
func (f *fakeStore) ListExecutions(ctx context.Context) ([]storage.ExecutionState, error) {
	return nil, nil
}

func (f *fakeStore) AppendEvent(ctx context.Context, event storage.Event) error {
	return nil
}

func (f *fakeStore) TransitionIssue(ctx context.Context, executionID, issueID string, to domain.IssueState) (domain.Issue, error) {
	f.closed = append(f.closed, issueID)
	return domain.Issue{}, nil
}

func specArtifact(state, approvedRevision string) *planning.Artifact {
	a := &planning.Artifact{
		Kind:     planning.KindSpec,
		Sections: []planning.Section{{Heading: "Objective", Body: "Build a widget"}},
		State:    state,
	}
	a.Revision = planning.ComputeRevision(a)
	a.ApprovedRevision = approvedRevision
	return a
}

func ticketPlanArtifact(state, approvedRevision string) *planning.Artifact {
	a := &planning.Artifact{
		Kind: planning.KindTicketPlan,
		Sections: []planning.Section{{
			Heading: "Ticket: T1",
			Body: "### Objective\nDo the thing\n\n" +
				"### Requirements\nREQ-001: covered\n\n" +
				"### Acceptance Criteria\n- it works\n",
		}},
		State: state,
	}
	a.Revision = planning.ComputeRevision(a)
	a.ApprovedRevision = approvedRevision
	return a
}

func TestApprovePlanningArtifact_NoExecutions_ReturnsError(t *testing.T) {
	artifacts := &fakeArtifacts{}
	store := &fakeStore{}
	approver := &planningapprove.Approver{Store: store, Artifacts: artifacts}

	err := approver.ApprovePlanningArtifact(context.Background(), "widget")
	if !errors.Is(err, planningapprove.ErrNoApprovableArtifact) {
		t.Fatalf("err = %v, want ErrNoApprovableArtifact", err)
	}
}

func TestApprovePlanningArtifact_LatestExecutionNotNeedsApproval_ReturnsError(t *testing.T) {
	artifacts := &fakeArtifacts{spec: specArtifact("reviewed", "")}
	store := &fakeStore{executions: []domain.PlanningExecution{
		{ID: "pe-1", FeatureID: "widget", Status: domain.PlanningStatusActive},
	}}
	approver := &planningapprove.Approver{Store: store, Artifacts: artifacts}

	err := approver.ApprovePlanningArtifact(context.Background(), "widget")
	if !errors.Is(err, planningapprove.ErrNoApprovableArtifact) {
		t.Fatalf("err = %v, want ErrNoApprovableArtifact", err)
	}
	if artifacts.spec.ApprovedRevision != "" {
		t.Fatalf("spec was approved despite execution not NEEDS_APPROVAL")
	}
}

func TestApprovePlanningArtifact_ApprovesPendingSpec(t *testing.T) {
	spec := specArtifact("reviewed", "")
	artifacts := &fakeArtifacts{spec: spec}
	store := &fakeStore{executions: []domain.PlanningExecution{
		{ID: "pe-1", FeatureID: "widget", Status: domain.PlanningStatusNeedsApproval},
	}}
	approver := &planningapprove.Approver{Store: store, Artifacts: artifacts}

	if err := approver.ApprovePlanningArtifact(context.Background(), "widget"); err != nil {
		t.Fatalf("ApprovePlanningArtifact: %v", err)
	}

	wantRev := planning.ComputeRevision(spec)
	if artifacts.spec.ApprovedRevision != wantRev {
		t.Fatalf("spec.ApprovedRevision = %q, want %q", artifacts.spec.ApprovedRevision, wantRev)
	}
	if artifacts.spec.State != "approved" {
		t.Fatalf("spec.State = %q, want %q", artifacts.spec.State, "approved")
	}
	if artifacts.ticketPlan != nil {
		t.Fatalf("ticket plan should not have been touched")
	}
}

func TestApprovePlanningArtifact_ApprovesPendingTicketPlanOverAlreadyApprovedSpec(t *testing.T) {
	spec := specArtifact("approved", "")
	spec.ApprovedRevision = planning.ComputeRevision(spec)
	tp := ticketPlanArtifact("reviewed", "")
	artifacts := &fakeArtifacts{spec: spec, ticketPlan: tp}
	store := &fakeStore{executions: []domain.PlanningExecution{
		{ID: "pe-1", FeatureID: "widget", Status: domain.PlanningStatusNeedsApproval},
	}}
	approver := &planningapprove.Approver{Store: store, Artifacts: artifacts}

	if err := approver.ApprovePlanningArtifact(context.Background(), "widget"); err != nil {
		t.Fatalf("ApprovePlanningArtifact: %v", err)
	}

	wantRev := planning.ComputeRevision(tp)
	if artifacts.ticketPlan.ApprovedRevision != wantRev {
		t.Fatalf("ticketPlan.ApprovedRevision = %q, want %q", artifacts.ticketPlan.ApprovedRevision, wantRev)
	}
	// The already-approved spec is left exactly as it was: only one Artifact
	// is ever pending approval at a time.
	if artifacts.spec.State != "approved" {
		t.Fatalf("spec.State changed to %q, want untouched %q", artifacts.spec.State, "approved")
	}
}

func TestApprovePlanningArtifact_BothArtifactsApproved_ReturnsError(t *testing.T) {
	spec := specArtifact("approved", "")
	spec.ApprovedRevision = planning.ComputeRevision(spec)
	tp := ticketPlanArtifact("approved", "")
	tp.ApprovedRevision = planning.ComputeRevision(tp)
	artifacts := &fakeArtifacts{spec: spec, ticketPlan: tp}
	store := &fakeStore{executions: []domain.PlanningExecution{
		{ID: "pe-1", FeatureID: "widget", Status: domain.PlanningStatusNeedsApproval},
	}}
	approver := &planningapprove.Approver{Store: store, Artifacts: artifacts}

	err := approver.ApprovePlanningArtifact(context.Background(), "widget")
	if !errors.Is(err, planningapprove.ErrNoApprovableArtifact) {
		t.Fatalf("err = %v, want ErrNoApprovableArtifact", err)
	}
}

func TestApprovePlanningArtifact_TicketPlanApproval_ResumesFrozenFeature(t *testing.T) {
	tp := ticketPlanArtifact("reviewed", "")
	artifacts := &fakeArtifacts{ticketPlan: tp}
	store := &fakeStore{
		frozen: true,
		executions: []domain.PlanningExecution{
			{ID: "pe-1", FeatureID: "widget", Status: domain.PlanningStatusNeedsApproval},
		},
	}
	approver := &planningapprove.Approver{Store: store, Artifacts: artifacts}

	if err := approver.ApprovePlanningArtifact(context.Background(), "widget"); err != nil {
		t.Fatalf("ApprovePlanningArtifact: %v", err)
	}
	if !store.unfrozen {
		t.Fatalf("expected feature to be unfrozen after ticket plan approval")
	}
}

func TestApprovePlanningArtifact_UnapprovedSpecAndTicketPlan_ApprovesSpecFirst(t *testing.T) {
	// A ticket plan can be unapproved not because the pipeline is waiting on
	// it, but because a spec that was already approved got hand-edited after
	// the ticket plan stage ran. In that case, the spec is the artifact
	// actually pending approval and must win over the stale ticket plan.
	spec := specArtifact("reviewed", "")
	tp := ticketPlanArtifact("reviewed", "")
	artifacts := &fakeArtifacts{spec: spec, ticketPlan: tp}
	store := &fakeStore{executions: []domain.PlanningExecution{
		{ID: "pe-1", FeatureID: "widget", Status: domain.PlanningStatusNeedsApproval},
	}}
	approver := &planningapprove.Approver{Store: store, Artifacts: artifacts}

	if err := approver.ApprovePlanningArtifact(context.Background(), "widget"); err != nil {
		t.Fatalf("ApprovePlanningArtifact: %v", err)
	}

	wantRev := planning.ComputeRevision(spec)
	if artifacts.spec.ApprovedRevision != wantRev {
		t.Fatalf("spec.ApprovedRevision = %q, want %q", artifacts.spec.ApprovedRevision, wantRev)
	}
	if artifacts.ticketPlan.ApprovedRevision != "" {
		t.Fatalf("ticket plan should not have been touched while the spec is still pending")
	}
}

func TestApproveSpec_ApprovesAtCurrentRevision(t *testing.T) {
	spec := specArtifact("reviewed", "")
	artifacts := &fakeArtifacts{spec: spec}
	approver := &planningapprove.Approver{Store: &fakeStore{}, Artifacts: artifacts}

	rev, err := approver.ApproveSpec(context.Background(), "widget")
	if err != nil {
		t.Fatalf("ApproveSpec: %v", err)
	}

	wantRev := planning.ComputeRevision(spec)
	if rev != wantRev {
		t.Fatalf("rev = %q, want %q", rev, wantRev)
	}
	if artifacts.spec.ApprovedRevision != wantRev {
		t.Fatalf("spec.ApprovedRevision = %q, want %q", artifacts.spec.ApprovedRevision, wantRev)
	}
	if artifacts.spec.State != "approved" {
		t.Fatalf("spec.State = %q, want %q", artifacts.spec.State, "approved")
	}
}

func TestApproveSpec_NoSpec_ReturnsError(t *testing.T) {
	approver := &planningapprove.Approver{Store: &fakeStore{}, Artifacts: &fakeArtifacts{}}

	if _, err := approver.ApproveSpec(context.Background(), "widget"); err == nil {
		t.Fatalf("expected an error when no spec exists")
	}
}

func TestApproveTicketPlan_ApprovesAtCurrentRevision_NotFrozen(t *testing.T) {
	tp := ticketPlanArtifact("reviewed", "")
	artifacts := &fakeArtifacts{ticketPlan: tp}
	store := &fakeStore{frozen: false}
	approver := &planningapprove.Approver{Store: store, Artifacts: artifacts}

	result, err := approver.ApproveTicketPlan(context.Background(), "widget")
	if err != nil {
		t.Fatalf("ApproveTicketPlan: %v", err)
	}

	wantRev := planning.ComputeRevision(tp)
	if result.Revision != wantRev {
		t.Fatalf("result.Revision = %q, want %q", result.Revision, wantRev)
	}
	if result.Resumed {
		t.Fatalf("result.Resumed = true, want false for an unfrozen feature")
	}
	if len(result.Superseded) != 0 {
		t.Fatalf("result.Superseded = %v, want empty", result.Superseded)
	}
	if artifacts.ticketPlan.ApprovedRevision != wantRev {
		t.Fatalf("ticketPlan.ApprovedRevision = %q, want %q", artifacts.ticketPlan.ApprovedRevision, wantRev)
	}
}

func TestApproveTicketPlan_ResumesFrozenFeature_ReportsSuperseded(t *testing.T) {
	tp := ticketPlanArtifact("reviewed", "")
	artifacts := &fakeArtifacts{ticketPlan: tp}
	store := &fakeStore{frozen: true}
	approver := &planningapprove.Approver{Store: store, Artifacts: artifacts}

	result, err := approver.ApproveTicketPlan(context.Background(), "widget")
	if err != nil {
		t.Fatalf("ApproveTicketPlan: %v", err)
	}
	if !result.Resumed {
		t.Fatalf("result.Resumed = false, want true for a frozen feature")
	}
	if !store.unfrozen {
		t.Fatalf("expected feature to be unfrozen")
	}
}

func TestApproveTicketPlan_NoTicketPlan_ReturnsError(t *testing.T) {
	approver := &planningapprove.Approver{Store: &fakeStore{}, Artifacts: &fakeArtifacts{}}

	if _, err := approver.ApproveTicketPlan(context.Background(), "widget"); err == nil {
		t.Fatalf("expected an error when no ticket plan exists")
	}
}

func TestApprovePlanningArtifact_SpecApproval_LeavesFeatureUnfrozenAlone(t *testing.T) {
	spec := specArtifact("reviewed", "")
	artifacts := &fakeArtifacts{spec: spec}
	store := &fakeStore{
		frozen: false,
		executions: []domain.PlanningExecution{
			{ID: "pe-1", FeatureID: "widget", Status: domain.PlanningStatusNeedsApproval},
		},
	}
	approver := &planningapprove.Approver{Store: store, Artifacts: artifacts}

	if err := approver.ApprovePlanningArtifact(context.Background(), "widget"); err != nil {
		t.Fatalf("ApprovePlanningArtifact: %v", err)
	}
	if store.unfrozen {
		t.Fatalf("expected no unfreeze call for a spec approval")
	}
}
