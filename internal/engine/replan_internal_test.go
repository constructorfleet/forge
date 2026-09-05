package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
)

// replanSpyStore is a minimal in-package storage.Store double covering only
// what handleReplanRequired touches, recording the order in which the
// side-effecting calls were made so the freeze-before-lease invariant can be
// asserted directly rather than inferred from Events.
type replanSpyStore struct {
	storage.Store // embed to satisfy Store; only the overridden methods are called

	checkpoint  *storage.ReplanCheckpoint
	freezes     []storage.FeatureFreeze
	state       domain.IssueState
	events      []storage.Event
	callOrder   []string
	freezeErr   error
	baseRevisio string
}

func (s *replanSpyStore) record(name string) { s.callOrder = append(s.callOrder, name) }

func (s *replanSpyStore) GetReplanCheckpoint(context.Context, string, string) (storage.ReplanCheckpoint, error) {
	if s.checkpoint == nil {
		return storage.ReplanCheckpoint{}, storage.ErrNotFound
	}
	return *s.checkpoint, nil
}

func (s *replanSpyStore) SaveReplanCheckpoint(_ context.Context, checkpoint storage.ReplanCheckpoint) error {
	cp := checkpoint
	s.checkpoint = &cp
	return nil
}

func (s *replanSpyStore) FreezeFeature(_ context.Context, featureID, reason, triggeringIssueID string) error {
	s.record("freeze")
	if s.freezeErr != nil {
		return s.freezeErr
	}
	s.freezes = append(s.freezes, storage.FeatureFreeze{
		FeatureID:         featureID,
		Reason:            reason,
		TriggeringIssueID: triggeringIssueID,
		CreatedAt:         time.Now().UTC(),
	})
	return nil
}

func (s *replanSpyStore) IsFeatureFrozen(_ context.Context, featureID string) (bool, storage.FeatureFreeze, error) {
	for _, f := range s.freezes {
		if f.FeatureID == featureID {
			return true, f, nil
		}
	}
	return false, storage.FeatureFreeze{}, nil
}

func (s *replanSpyStore) LoadExecution(context.Context, string) (storage.ExecutionState, error) {
	return storage.ExecutionState{Execution: domain.Execution{ID: "exec-1", BaseRevision: s.baseRevisio}}, nil
}

func (s *replanSpyStore) AppendEvent(_ context.Context, event storage.Event) error {
	s.events = append(s.events, event)
	return nil
}

func (s *replanSpyStore) ReleaseWorkerClaim(context.Context, string, string) error { return nil }

func (s *replanSpyStore) TransitionIssue(_ context.Context, _, _ string, to domain.IssueState) (domain.Issue, error) {
	s.state = to
	return domain.Issue{State: to}, nil
}

func (s *replanSpyStore) GetIssue(context.Context, string, string) (domain.Issue, error) {
	return domain.Issue{State: s.state}, nil
}

// orderRecordingLease is a PlanningLeaseAcquirer that records its own call
// into the shared store call log.
type orderRecordingLease struct {
	store *replanSpyStore
	err   error
}

func (l *orderRecordingLease) Start(context.Context, string, string) (domain.PlanningExecution, error) {
	l.store.record("lease")
	if l.err != nil {
		return domain.PlanningExecution{}, l.err
	}
	return domain.PlanningExecution{ID: "plan-exec-1"}, nil
}

// orderRecordingDecisions is a ReplanDecisionRecorder that records its own
// call into the shared store call log.
type orderRecordingDecisions struct {
	store *replanSpyStore
	calls int
}

func (d *orderRecordingDecisions) RecordReplanTrigger(context.Context, string, string, string, agent.ReplanDetail) (string, error) {
	d.store.record("decision")
	d.calls++
	return "007-replan", nil
}

func replanTestIssue() domain.Issue {
	return domain.Issue{
		ID: "7",
		Body: "### Objective\ndo it\n\n" + tracker.RenderForgeProvenance(tracker.ForgeProvenance{
			Status:       tracker.ProvenanceReady,
			Project:      "feature-1",
			SpecRevision: "spec-rev",
			PlanRevision: "plan-rev-1",
		}),
	}
}

func replanTestDetail() agent.AgentResult {
	return agent.AgentResult{
		Status:  agent.StatusReplanRequired,
		Summary: "plan invalid",
		Replan: &agent.ReplanDetail{
			Reason:            "the plan assumes an API that does not exist",
			Evidence:          "client.go only streams",
			SuggestedQuestion: "adopt streaming?",
		},
	}
}

func newReplanEngine(store *replanSpyStore, trk NeedsInfoTracker) *Engine {
	return &Engine{
		Store:            store,
		Config:           config.Default(),
		NeedsInfoTracker: trk,
		Now:              func() time.Time { return time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC) },
	}
}

// TestHandleReplanRequired_FreezesStrictlyBeforeLeaseAndDecision asserts the
// side-effect order directly at the Store/seam boundary: the freeze write
// must happen before the planning lease is acquired, and the Decision is
// only recorded once both have.
func TestHandleReplanRequired_FreezesStrictlyBeforeLeaseAndDecision(t *testing.T) {
	store := &replanSpyStore{}
	eng := newReplanEngine(store, &countingTracker{})
	lease := &orderRecordingLease{store: store}
	decisions := &orderRecordingDecisions{store: store}
	eng.PlanningLease = lease
	eng.ReplanDecisions = decisions

	if _, err := eng.handleReplanRequired(context.Background(), "exec-1", "7", "worker-1", replanTestIssue(), replanTestDetail()); err != nil {
		t.Fatalf("handleReplanRequired: %v", err)
	}

	want := []string{"freeze", "lease", "decision"}
	if len(store.callOrder) != len(want) {
		t.Fatalf("call order = %v, want %v", store.callOrder, want)
	}
	for i, name := range want {
		if store.callOrder[i] != name {
			t.Fatalf("call order = %v, want %v", store.callOrder, want)
		}
	}
	if store.state != domain.StateNeedsReplan {
		t.Errorf("final state = %s, want NEEDS_REPLAN", store.state)
	}
}

// TestHandleReplanRequired_FreezeSurvivesLeaseFailure is the ordering
// invariant's real payoff: an unrecoverable lease failure aborts the
// escalation, but the freeze it already wrote stays in place, so the Feature
// is never left open for new work against a plan known to be invalid.
func TestHandleReplanRequired_FreezeSurvivesLeaseFailure(t *testing.T) {
	store := &replanSpyStore{}
	eng := newReplanEngine(store, &countingTracker{})
	eng.PlanningLease = &orderRecordingLease{store: store, err: errors.New("database is gone")}
	decisions := &orderRecordingDecisions{store: store}
	eng.ReplanDecisions = decisions

	_, err := eng.handleReplanRequired(context.Background(), "exec-1", "7", "worker-1", replanTestIssue(), replanTestDetail())
	if err == nil {
		t.Fatal("expected an unrecoverable lease failure to fail the escalation")
	}

	frozen, freeze, ferr := store.IsFeatureFrozen(context.Background(), "feature-1")
	if ferr != nil {
		t.Fatalf("IsFeatureFrozen: %v", ferr)
	}
	if !frozen {
		t.Fatal("the freeze must survive a failed lease acquisition")
	}
	if freeze.TriggeringIssueID != "7" {
		t.Errorf("freeze = %+v", freeze)
	}
	if store.checkpoint == nil || !store.checkpoint.Frozen {
		t.Error("the checkpoint must record that the freeze already happened")
	}
	if decisions.calls != 0 {
		t.Error("the Decision must not be recorded once the escalation aborts")
	}
}

// TestHandleReplanRequired_IdempotentOnRepeat calls the handler twice to
// completion for the same Execution/Issue and asserts the label and comment
// are each posted exactly once, mirroring
// TestHandleNeedsInfo_IdempotentOnRepeat.
func TestHandleReplanRequired_IdempotentOnRepeat(t *testing.T) {
	store := &replanSpyStore{}
	trk := &countingTracker{}
	eng := newReplanEngine(store, trk)
	decisions := &orderRecordingDecisions{store: store}
	eng.ReplanDecisions = decisions

	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := eng.handleReplanRequired(ctx, "exec-1", "7", "worker-1", replanTestIssue(), replanTestDetail()); err != nil {
			t.Fatalf("handleReplanRequired call %d: %v", i+1, err)
		}
	}

	if trk.commentCalls != 1 {
		t.Errorf("comment calls = %d, want 1", trk.commentCalls)
	}
	// AddLabel is idempotent per tracker.Tracker's contract, so it is called
	// on every invocation by design — the same convention handleNeedsInfo
	// documents.
	if trk.labelCalls != 2 {
		t.Errorf("label calls = %d, want 2 (AddLabel is contractually idempotent)", trk.labelCalls)
	}
	if store.checkpoint == nil || !store.checkpoint.CommentPosted {
		t.Error("checkpoint did not record the posted comment")
	}
}

// TestHandleReplanRequired_ToleratesLeaseConflict pins the one lease failure
// that is not fatal: another Planning Execution already owns replanning this
// Feature, which is the state the escalation wanted anyway.
func TestHandleReplanRequired_ToleratesLeaseConflict(t *testing.T) {
	store := &replanSpyStore{}
	eng := newReplanEngine(store, &countingTracker{})
	eng.PlanningLease = &orderRecordingLease{
		store: store,
		err:   &storage.PlanningLeaseConflictError{FeatureID: "feature-1", OwningExecutionID: "other"},
	}
	decisions := &orderRecordingDecisions{store: store}
	eng.ReplanDecisions = decisions

	if _, err := eng.handleReplanRequired(context.Background(), "exec-1", "7", "worker-1", replanTestIssue(), replanTestDetail()); err != nil {
		t.Fatalf("handleReplanRequired: %v", err)
	}
	if store.state != domain.StateNeedsReplan {
		t.Errorf("final state = %s, want NEEDS_REPLAN", store.state)
	}
	if decisions.calls != 1 {
		t.Errorf("decision calls = %d, want 1", decisions.calls)
	}
	if store.checkpoint.LeaseExecutionID != "" {
		t.Errorf("no lease was taken, so LeaseExecutionID must stay empty: %q", store.checkpoint.LeaseExecutionID)
	}
}

// revalidateSpyStore is a minimal in-package storage.Store double covering
// only what revalidateAfterReplan touches.
type revalidateSpyStore struct {
	storage.Store // embed to satisfy Store; only the overridden methods are called

	workspace domain.Workspace
	gateRuns  []storage.GateRun
}

func (s *revalidateSpyStore) WorkspaceByIssue(context.Context, string, string) (domain.Workspace, error) {
	return s.workspace, nil
}

func (s *revalidateSpyStore) RecordGateRun(_ context.Context, run storage.GateRun) error {
	s.gateRuns = append(s.gateRuns, run)
	return nil
}

func (s *revalidateSpyStore) AgentRunsByIssue(context.Context, string, string) ([]storage.AgentRun, error) {
	return nil, nil
}

// newRevalidateEngine builds an Engine that revalidates gates inside dir.
func newRevalidateEngine(dir string, gates []config.QualityGate) (*Engine, *revalidateSpyStore) {
	store := &revalidateSpyStore{workspace: domain.Workspace{Path: dir}}
	cfg := config.Default()
	cfg.Quality.Gates = gates
	return &Engine{
		Store:      store,
		Workspaces: &recordingWorkspaceCreator{},
		Config:     cfg,
		Now:        func() time.Time { return time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC) },
	}, store
}

// TestRevalidateAfterReplan_RunsGatesThroughTheEnvironment covers ticket
// 305 for the replan path: revalidation runs each gate through the
// environment and records the result.
func TestRevalidateAfterReplan_RunsGatesThroughTheEnvironment(t *testing.T) {
	eng, store := newRevalidateEngine(t.TempDir(), []config.QualityGate{
		{Name: "build", Command: "echo built"},
	})

	passed, detail, err := eng.revalidateAfterReplan(context.Background(), "exec-1", "7")
	if err != nil {
		t.Fatalf("revalidateAfterReplan: %v", err)
	}
	if !passed {
		t.Errorf("passed = false, want true (detail: %s)", detail)
	}
	if len(store.gateRuns) != 1 {
		t.Fatalf("gate runs = %+v, want exactly one", store.gateRuns)
	}
	run := store.gateRuns[0]
	if run.Name != "build" || run.Command != "echo built" || !run.Passed || run.ExitCode != 0 {
		t.Errorf("gate run = %+v, want a passing build gate", run)
	}
	if run.Stdout != "built\n" {
		t.Errorf("Stdout = %q, want the command output", run.Stdout)
	}
	if run.ExecutionID != "exec-1" || run.IssueID != "7" {
		t.Errorf("gate run = %+v, want it keyed by (exec-1, 7)", run)
	}
}

// TestRevalidateAfterReplan_StopsAtTheFirstFailingGate pins the unchanged
// stop-on-first-fail behavior: a failing gate ends revalidation, so the
// later gates never run.
func TestRevalidateAfterReplan_StopsAtTheFirstFailingGate(t *testing.T) {
	eng, store := newRevalidateEngine(t.TempDir(), []config.QualityGate{
		{Name: "build", Command: "exit 2"},
		{Name: "test", Command: "echo tested"},
	})

	passed, detail, err := eng.revalidateAfterReplan(context.Background(), "exec-1", "7")
	if err != nil {
		t.Fatalf("revalidateAfterReplan: %v", err)
	}
	if passed {
		t.Error("passed = true, want false")
	}
	if !strings.Contains(detail, "build") {
		t.Errorf("detail = %q, want it to name the failing gate", detail)
	}
	if len(store.gateRuns) != 1 {
		t.Fatalf("gate runs = %+v, want only the failing one", store.gateRuns)
	}
	if store.gateRuns[0].ExitCode != 2 || store.gateRuns[0].Passed {
		t.Errorf("gate run = %+v, want a failing build gate", store.gateRuns[0])
	}
}

// TestRevalidateAfterReplan_ReportsAnEmptyGateSet pins the no-gates case:
// there is nothing to revalidate, so the Issue still passes.
func TestRevalidateAfterReplan_ReportsAnEmptyGateSet(t *testing.T) {
	eng, store := newRevalidateEngine(t.TempDir(), nil)

	passed, detail, err := eng.revalidateAfterReplan(context.Background(), "exec-1", "7")
	if err != nil {
		t.Fatalf("revalidateAfterReplan: %v", err)
	}
	if !passed {
		t.Error("passed = false, want true")
	}
	if detail != "no quality gates configured to revalidate against" {
		t.Errorf("detail = %q", detail)
	}
	if len(store.gateRuns) != 0 {
		t.Errorf("gate runs = %+v, want none", store.gateRuns)
	}
}
