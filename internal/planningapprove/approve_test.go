package planningapprove_test

import (
	"context"
	"errors"
	"reflect"
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

// spyLocker is a Locker double that records every resource it was asked to
// lock and runs fn synchronously (no real filesystem lock, no serialization
// -- Approver's own tests only need to prove *which* resource it locks and
// that it locks around the load-mutate-save sequence exactly once, not that
// locking itself excludes concurrent callers; that guarantee belongs to
// repolock's own tests).
type spyLocker struct {
	calls []string
}

func (s *spyLocker) WithLock(ctx context.Context, resource string, fn func() error) error {
	s.calls = append(s.calls, resource)
	return fn()
}

func specArtifact(state, approvedRevision string) *planning.Artifact {
	a := &planning.Artifact{
		Kind:     planning.KindSpec,
		Sections: []planning.Section{{Heading: "Objective", Body: "Build a widget"}},
		State:    state,
	}
	a.Revision = planning.ComputeRevision(a)
	a.ApprovedRevision = approvedRevision
	if state == "reviewed" {
		a.ReviewedRevision = a.Revision
	}
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
	if state == "reviewed" {
		a.ReviewedRevision = a.Revision
	}
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

func TestApprovePlanningArtifact_PendingSpecNotReviewed_ReturnsError(t *testing.T) {
	spec := specArtifact("draft", "")
	artifacts := &fakeArtifacts{spec: spec}
	store := &fakeStore{executions: []domain.PlanningExecution{
		{ID: "pe-1", FeatureID: "widget", Status: domain.PlanningStatusNeedsApproval},
	}}
	approver := &planningapprove.Approver{Store: store, Artifacts: artifacts}

	err := approver.ApprovePlanningArtifact(context.Background(), "widget")
	if !errors.Is(err, planningapprove.ErrArtifactNotReviewed) {
		t.Fatalf("err = %v, want ErrArtifactNotReviewed", err)
	}
	if artifacts.spec.ApprovedRevision != "" {
		t.Fatalf("spec was approved despite failing to pass automated review")
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

func TestApproveSpec_NotReviewed_ReturnsError(t *testing.T) {
	spec := specArtifact("draft", "")
	artifacts := &fakeArtifacts{spec: spec}
	approver := &planningapprove.Approver{Store: &fakeStore{}, Artifacts: artifacts}

	_, err := approver.ApproveSpec(context.Background(), "widget")
	if !errors.Is(err, planningapprove.ErrArtifactNotReviewed) {
		t.Fatalf("err = %v, want ErrArtifactNotReviewed", err)
	}
	if artifacts.spec.ApprovedRevision != "" {
		t.Fatalf("spec was approved despite failing to pass automated review")
	}
}

func TestApproveSpec_ReviewedContentEditedSinceReview_ReturnsError(t *testing.T) {
	spec := specArtifact("reviewed", "")
	// A hand-edit after review changes the content but leaves State
	// "reviewed" untouched, exactly the gap #473 was filed to close.
	spec.Sections[0].Body = "Build a different widget"
	spec.Revision = planning.ComputeRevision(spec)
	artifacts := &fakeArtifacts{spec: spec}
	approver := &planningapprove.Approver{Store: &fakeStore{}, Artifacts: artifacts}

	_, err := approver.ApproveSpec(context.Background(), "widget")
	if !errors.Is(err, planningapprove.ErrArtifactNotReviewed) {
		t.Fatalf("err = %v, want ErrArtifactNotReviewed", err)
	}
	if artifacts.spec.ApprovedRevision != "" {
		t.Fatalf("spec was approved despite being edited since its last review")
	}
}

func TestApproveSpec_LegacyNeverTouchedByReviewTracking_Approves(t *testing.T) {
	// A spec.md written before review-tracking existed has empty State and
	// empty ReviewedRevision, yet it could only have reached NEEDS_APPROVAL
	// by already passing SpecificationReview under the code that wrote it.
	// Approval must treat it as reviewed rather than permanently refusing
	// it (#473 follow-up: legacy artifacts must not become dead ends).
	spec := specArtifact("", "")
	artifacts := &fakeArtifacts{spec: spec}
	approver := &planningapprove.Approver{Store: &fakeStore{}, Artifacts: artifacts}

	rev, err := approver.ApproveSpec(context.Background(), "widget")
	if err != nil {
		t.Fatalf("ApproveSpec: %v", err)
	}
	if artifacts.spec.ApprovedRevision != rev {
		t.Fatalf("spec.ApprovedRevision = %q, want %q", artifacts.spec.ApprovedRevision, rev)
	}
	if artifacts.spec.ReviewedRevision != rev {
		t.Fatalf("spec.ReviewedRevision = %q, want %q (legacy artifact should be backfilled)", artifacts.spec.ReviewedRevision, rev)
	}
}

func TestApproveSpec_ChangesRequired_ReturnsError(t *testing.T) {
	spec := specArtifact("changes_required", "")
	artifacts := &fakeArtifacts{spec: spec}
	approver := &planningapprove.Approver{Store: &fakeStore{}, Artifacts: artifacts}

	_, err := approver.ApproveSpec(context.Background(), "widget")
	if !errors.Is(err, planningapprove.ErrArtifactNotReviewed) {
		t.Fatalf("err = %v, want ErrArtifactNotReviewed", err)
	}
}

func TestApproveSpec_LocksArtifactMutationOnce(t *testing.T) {
	spec := specArtifact("reviewed", "")
	artifacts := &fakeArtifacts{spec: spec}
	locks := &spyLocker{}
	approver := &planningapprove.Approver{Store: &fakeStore{}, Artifacts: artifacts, Locks: locks}

	if _, err := approver.ApproveSpec(context.Background(), "widget"); err != nil {
		t.Fatalf("ApproveSpec: %v", err)
	}

	if want := []string{"planning:widget"}; !reflect.DeepEqual(locks.calls, want) {
		t.Fatalf("locks.calls = %v, want %v", locks.calls, want)
	}
}

func TestApproveTicketPlan_LocksArtifactMutationOnce(t *testing.T) {
	tp := ticketPlanArtifact("reviewed", "")
	artifacts := &fakeArtifacts{ticketPlan: tp}
	locks := &spyLocker{}
	approver := &planningapprove.Approver{Store: &fakeStore{}, Artifacts: artifacts, Locks: locks}

	if _, err := approver.ApproveTicketPlan(context.Background(), "widget"); err != nil {
		t.Fatalf("ApproveTicketPlan: %v", err)
	}

	if want := []string{"planning:widget"}; !reflect.DeepEqual(locks.calls, want) {
		t.Fatalf("locks.calls = %v, want %v", locks.calls, want)
	}
}

func TestApprovePlanningArtifact_LocksArtifactMutationOnce(t *testing.T) {
	spec := specArtifact("reviewed", "")
	artifacts := &fakeArtifacts{spec: spec}
	locks := &spyLocker{}
	store := &fakeStore{executions: []domain.PlanningExecution{
		{ID: "pe-1", FeatureID: "widget", Status: domain.PlanningStatusNeedsApproval},
	}}
	approver := &planningapprove.Approver{Store: store, Artifacts: artifacts, Locks: locks}

	if err := approver.ApprovePlanningArtifact(context.Background(), "widget"); err != nil {
		t.Fatalf("ApprovePlanningArtifact: %v", err)
	}

	// Exactly one lock acquisition for the whole decide-and-approve
	// sequence: nesting a second WithLock call for the same resource inside
	// approveSpec/approveTicketPlan would deadlock against repolock's real,
	// non-reentrant file lock.
	if want := []string{"planning:widget"}; !reflect.DeepEqual(locks.calls, want) {
		t.Fatalf("locks.calls = %v, want %v", locks.calls, want)
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

func TestApproveTicketPlan_NotReviewed_ReturnsError(t *testing.T) {
	tp := ticketPlanArtifact("draft", "")
	artifacts := &fakeArtifacts{ticketPlan: tp}
	approver := &planningapprove.Approver{Store: &fakeStore{}, Artifacts: artifacts}

	_, err := approver.ApproveTicketPlan(context.Background(), "widget")
	if !errors.Is(err, planningapprove.ErrArtifactNotReviewed) {
		t.Fatalf("err = %v, want ErrArtifactNotReviewed", err)
	}
	if artifacts.ticketPlan.ApprovedRevision != "" {
		t.Fatalf("ticket plan was approved despite failing to pass automated review")
	}
}

func TestApproveTicketPlan_LegacyNeverTouchedByReviewTracking_Approves(t *testing.T) {
	// Same legacy gap as TestApproveSpec_LegacyNeverTouchedByReviewTracking_Approves,
	// for the ticket-plan artifact kind.
	tp := ticketPlanArtifact("", "")
	artifacts := &fakeArtifacts{ticketPlan: tp}
	approver := &planningapprove.Approver{Store: &fakeStore{}, Artifacts: artifacts}

	result, err := approver.ApproveTicketPlan(context.Background(), "widget")
	if err != nil {
		t.Fatalf("ApproveTicketPlan: %v", err)
	}
	if artifacts.ticketPlan.ApprovedRevision != result.Revision {
		t.Fatalf("ticketPlan.ApprovedRevision = %q, want %q", artifacts.ticketPlan.ApprovedRevision, result.Revision)
	}
	if artifacts.ticketPlan.ReviewedRevision != result.Revision {
		t.Fatalf("ticketPlan.ReviewedRevision = %q, want %q (legacy artifact should be backfilled)", artifacts.ticketPlan.ReviewedRevision, result.Revision)
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
