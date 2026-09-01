package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/gittest"
	"github.com/Teagan42/forge/internal/needsinfo"
	"github.com/Teagan42/forge/internal/review"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
	"github.com/Teagan42/forge/internal/workspace"
)

const replanFeatureID = "widget-feature"

// materializedBody renders an Issue body carrying the ready Forge
// Provenance stamp a materialized Issue has, which is where the engine
// reads the Feature ID and ticket plan revision from.
func materializedBody(planRevision string, requirements ...string) string {
	return "### Objective\ndo the thing\n\n" + tracker.RenderForgeProvenance(tracker.ForgeProvenance{
		Status:       tracker.ProvenanceReady,
		TempKey:      "TKT-001",
		Project:      replanFeatureID,
		SpecRevision: "spec-rev-1",
		PlanRevision: planRevision,
		Requirements: requirements,
	})
}

func replanResult() agent.AgentResult {
	return agent.AgentResult{
		Status:  agent.StatusReplanRequired,
		Summary: "the plan cannot be implemented as written",
		Replan: &agent.ReplanDetail{
			Reason:               "the plan assumes a synchronous API that does not exist",
			Evidence:             "internal/api/client.go exposes only a streaming interface",
			AffectedRequirements: []string{"REQ-3", "REQ-4"},
			SuggestedQuestion:    "should the feature adopt the streaming interface?",
		},
	}
}

// spyPlanningLease is an engine.PlanningLeaseAcquirer double that records
// when it was called relative to the Feature freeze, and can be programmed
// to fail.
type spyPlanningLease struct {
	store storage.Store
	err   error

	mu sync.Mutex
	// frozenAtCall records whether the Feature was already frozen at the
	// moment Start was invoked — the freeze-before-lease ordering assertion.
	frozenAtCall []bool
}

func (s *spyPlanningLease) Start(ctx context.Context, featureID, baseRevision string) (domain.PlanningExecution, error) {
	frozen, _, err := s.store.IsFeatureFrozen(ctx, featureID)
	if err != nil {
		return domain.PlanningExecution{}, err
	}
	s.mu.Lock()
	s.frozenAtCall = append(s.frozenAtCall, frozen)
	s.mu.Unlock()

	if s.err != nil {
		return domain.PlanningExecution{}, s.err
	}
	return domain.PlanningExecution{ID: "plan-exec-1", FeatureID: featureID, BaseRevision: baseRevision}, nil
}

func (s *spyPlanningLease) calls() []bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]bool(nil), s.frozenAtCall...)
}

// spyDecisionRecorder is an engine.ReplanDecisionRecorder double.
type spyDecisionRecorder struct {
	err error

	mu    sync.Mutex
	calls []struct {
		featureID, issueID, planRevision string
		detail                           agent.ReplanDetail
	}
}

func (s *spyDecisionRecorder) RecordReplanTrigger(_ context.Context, featureID, issueID, planRevision string, detail agent.ReplanDetail) (string, error) {
	s.mu.Lock()
	s.calls = append(s.calls, struct {
		featureID, issueID, planRevision string
		detail                           agent.ReplanDetail
	}{featureID, issueID, planRevision, detail})
	s.mu.Unlock()
	if s.err != nil {
		return "", s.err
	}
	return "007-replan-transport", nil
}

func (s *spyDecisionRecorder) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

// freezingAgent reports StatusImplemented but freezes the Feature as a side
// effect of running, standing in for a *different* Worker escalating
// REPLAN_REQUIRED while this one is still in flight.
type freezingAgent struct {
	freeze func() error
}

func (a *freezingAgent) Execute(context.Context, agent.AgentRequest) (agent.AgentResult, error) {
	if err := a.freeze(); err != nil {
		return agent.AgentResult{}, err
	}
	return agent.AgentResult{Status: agent.StatusImplemented}, nil
}

type replanTestEngine struct {
	eng      *engine.Engine
	store    *storage.SQLiteStore
	trk      *fakeTracker
	fake     *agent.FakeAgent
	base     string
	lease    *spyPlanningLease
	decision *spyDecisionRecorder
}

func newReplanTestEngine(t *testing.T, issues map[string]domain.Issue) replanTestEngine {
	t.Helper()
	repoRoot, base := gittest.NewTempRepo(t)
	store := openTestStore(t)
	trk := newFakeTracker()
	for id, issue := range issues {
		trk.issues[id] = issue
	}
	mgr, err := workspace.NewManager(repoRoot)
	if err != nil {
		t.Fatalf("workspace.NewManager: %v", err)
	}
	fake := agent.NewFakeAgent()
	eng := engine.New(store, trk, mgr, fake, config.Default(), repoRoot)
	eng.NeedsInfoTracker = trk
	lease := &spyPlanningLease{store: store}
	decision := &spyDecisionRecorder{}
	eng.PlanningLease = lease
	eng.ReplanDecisions = decision
	return replanTestEngine{eng: eng, store: store, trk: trk, fake: fake, base: base, lease: lease, decision: decision}
}

func eventTypes(t *testing.T, store *storage.SQLiteStore, executionID string) []string {
	t.Helper()
	events, err := store.EventsByExecution(context.Background(), executionID)
	if err != nil {
		t.Fatalf("EventsByExecution: %v", err)
	}
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Type)
	}
	return out
}

func indexOf(types []string, want string) int {
	for i, tp := range types {
		if tp == want {
			return i
		}
	}
	return -1
}

// TestExecute_ReplanRequired_FreezesReopensAndParksIssue is this ticket's
// headline integration test (acceptance items 1 and 2): a Worker returning
// REPLAN_REQUIRED parks the Issue in NEEDS_REPLAN, freezes the Feature,
// takes the planning lease, records the Decision, labels and comments on the
// Issue, and checkpoints all of it — with the Workspace preserved and no
// pull request created.
func TestExecute_ReplanRequired_FreezesReopensAndParksIssue(t *testing.T) {
	te := newReplanTestEngine(t, map[string]domain.Issue{
		"7": {ID: "7", Body: materializedBody("plan-rev-1", "REQ-3", "REQ-4")},
	})
	te.fake.ProgramResult("7", replanResult())

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "7", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateNeedsReplan {
		t.Fatalf("final state = %s, want NEEDS_REPLAN", result.Issue.State)
	}

	frozen, freeze, err := te.store.IsFeatureFrozen(ctx, replanFeatureID)
	if err != nil {
		t.Fatalf("IsFeatureFrozen: %v", err)
	}
	if !frozen {
		t.Fatal("REPLAN_REQUIRED did not freeze the Feature")
	}
	if freeze.TriggeringIssueID != "7" {
		t.Errorf("freeze.TriggeringIssueID = %q, want 7", freeze.TriggeringIssueID)
	}
	if !strings.Contains(freeze.Reason, "synchronous API") {
		t.Errorf("freeze.Reason = %q", freeze.Reason)
	}

	if calls := te.lease.calls(); len(calls) != 1 {
		t.Fatalf("planning lease Start called %d times, want 1", len(calls))
	} else if !calls[0] {
		t.Error("planning lease was acquired before the Feature was frozen")
	}

	if te.decision.callCount() != 1 {
		t.Fatalf("decision recorder called %d times, want 1", te.decision.callCount())
	}
	call := te.decision.calls[0]
	if call.featureID != replanFeatureID || call.issueID != "7" || call.planRevision != "plan-rev-1" {
		t.Errorf("decision recorded with %+v", call)
	}
	if call.detail.SuggestedQuestion != "should the feature adopt the streaming interface?" {
		t.Errorf("decision detail did not carry the suggested question: %+v", call.detail)
	}

	checkpoint, err := te.store.GetReplanCheckpoint(ctx, result.ExecutionID, "7")
	if err != nil {
		t.Fatalf("GetReplanCheckpoint: %v", err)
	}
	if checkpoint.FeatureID != replanFeatureID || checkpoint.PlanRevision != "plan-rev-1" {
		t.Errorf("checkpoint = %+v", checkpoint)
	}
	if !checkpoint.Frozen {
		t.Error("checkpoint.Frozen = false")
	}
	if checkpoint.LeaseExecutionID != "plan-exec-1" {
		t.Errorf("checkpoint.LeaseExecutionID = %q", checkpoint.LeaseExecutionID)
	}
	if checkpoint.DecisionID != "007-replan-transport" {
		t.Errorf("checkpoint.DecisionID = %q", checkpoint.DecisionID)
	}
	if len(checkpoint.AffectedRequirements) != 2 {
		t.Errorf("checkpoint.AffectedRequirements = %v", checkpoint.AffectedRequirements)
	}
	if !checkpoint.CommentPosted || checkpoint.CommentAuthor != botAuthor {
		t.Errorf("checkpoint comment state = %+v", checkpoint)
	}

	wantLabel := config.Default().Blocked.Label
	if labels := te.trk.Labels("7"); len(labels) != 1 || labels[0] != wantLabel {
		t.Errorf("labels = %v, want [%s]", labels, wantLabel)
	}
	if te.trk.CommentCount("7") != 1 {
		t.Fatalf("comment count = %d, want 1", te.trk.CommentCount("7"))
	}
	comments, err := te.trk.GetComments(ctx, "7")
	if err != nil {
		t.Fatalf("GetComments: %v", err)
	}
	body := comments[0].Body
	for _, want := range []string{
		"the plan assumes a synchronous API that does not exist",
		"internal/api/client.go exposes only a streaming interface",
		"REQ-3, REQ-4",
		"should the feature adopt the streaming interface?",
		"Completed work is retained",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("comment missing %q:\n%s", want, body)
		}
	}
	if !needsinfo.IsForgeComment(body, result.ExecutionID, "7") {
		t.Errorf("replan comment body does not carry forge's hidden marker: %s", body)
	}

	// Ordering: the freeze event must precede the lease event, which must
	// precede the decision event.
	types := eventTypes(t, te.store, result.ExecutionID)
	freezeIdx := indexOf(types, "replan.feature_frozen")
	leaseIdx := indexOf(types, "replan.lease_acquired")
	decisionIdx := indexOf(types, "replan.decision_opened")
	checkpointIdx := indexOf(types, "replan.checkpoint_saved")
	if freezeIdx < 0 || leaseIdx < 0 || decisionIdx < 0 || checkpointIdx < 0 {
		t.Fatalf("missing replan events in %v", types)
	}
	if freezeIdx >= leaseIdx || leaseIdx >= decisionIdx || decisionIdx >= checkpointIdx {
		t.Errorf("replan events out of order: %v", types)
	}
	if idx := indexOf(types, "pr.created"); idx >= 0 {
		t.Error("a pull request event was recorded for a REPLAN_REQUIRED issue")
	}

	// The Workspace is preserved: nothing is cleaned up on this path.
	if _, err := te.store.WorkspaceByIssue(ctx, result.ExecutionID, "7"); err != nil {
		t.Errorf("WorkspaceByIssue: %v (the workspace must be preserved)", err)
	}
}

// TestExecute_ReplanRequired_FreezesBeforeLeaseEvenWhenLeaseConflicts is the
// explicit freeze-before-lease ordering assertion (acceptance item 2): the
// freeze is durable even when acquiring the planning lease conflicts,
// because a Feature that took the lease but not the freeze would keep
// dispatching Workers against a plan already known to be invalid.
func TestExecute_ReplanRequired_FreezesBeforeLeaseEvenWhenLeaseConflicts(t *testing.T) {
	te := newReplanTestEngine(t, map[string]domain.Issue{
		"7": {ID: "7", Body: materializedBody("plan-rev-1")},
	})
	te.lease.err = &storage.PlanningLeaseConflictError{
		FeatureID:         replanFeatureID,
		OwningExecutionID: "someone-else",
	}
	te.fake.ProgramResult("7", replanResult())

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "7", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateNeedsReplan {
		t.Fatalf("final state = %s, want NEEDS_REPLAN", result.Issue.State)
	}

	frozen, _, err := te.store.IsFeatureFrozen(ctx, replanFeatureID)
	if err != nil {
		t.Fatalf("IsFeatureFrozen: %v", err)
	}
	if !frozen {
		t.Fatal("a conflicting planning lease must not leave the Feature unfrozen")
	}
	if calls := te.lease.calls(); len(calls) != 1 || !calls[0] {
		t.Fatalf("lease Start observed frozen = %v, want [true]", calls)
	}

	types := eventTypes(t, te.store, result.ExecutionID)
	if indexOf(types, "replan.lease_conflict") < 0 {
		t.Errorf("no replan.lease_conflict event recorded: %v", types)
	}
	// The Decision is still recorded: a lease held elsewhere does not stop
	// the trigger from being written down.
	if te.decision.callCount() != 1 {
		t.Errorf("decision recorder called %d times, want 1", te.decision.callCount())
	}
}

// TestExecute_FrozenFeature_RefusesToStartNewWork is acceptance item 2's
// scheduling half: while a Feature is frozen, an Issue belonging to it is
// never admitted, so no Issue row, claim, or Workspace is created for it.
func TestExecute_FrozenFeature_RefusesToStartNewWork(t *testing.T) {
	te := newReplanTestEngine(t, map[string]domain.Issue{
		"8": {ID: "8", Body: materializedBody("plan-rev-1")},
	})
	te.fake.ProgramResult("8", agent.AgentResult{Status: agent.StatusImplemented})

	ctx := context.Background()
	if err := te.store.FreezeFeature(ctx, replanFeatureID, "plan invalidated", "7"); err != nil {
		t.Fatalf("FreezeFeature: %v", err)
	}

	_, err := te.eng.Execute(ctx, "8", te.base)
	if err == nil {
		t.Fatal("expected Execute to refuse an Issue of a frozen Feature")
	}
	var frozenErr *engine.FeatureFrozenError
	if !errors.As(err, &frozenErr) {
		t.Fatalf("error = %T (%v), want *engine.FeatureFrozenError", err, err)
	}
	if frozenErr.FeatureID != replanFeatureID || frozenErr.IssueID != "8" {
		t.Errorf("error = %+v", frozenErr)
	}
	if len(te.fake.Invocations()) != 0 {
		t.Error("the Agent was invoked for an Issue of a frozen Feature")
	}
	if _, err := te.store.GetIssue(ctx, "any", "8"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("a refused dispatch must leave no Issue row: %v", err)
	}
}

// TestExecute_UnstampedIssueIsNeverFrozen guards the compatibility case: an
// Issue with no Forge Provenance block belongs to no Feature, so no Feature
// freeze can apply to it.
func TestExecute_UnstampedIssueIsNeverFrozen(t *testing.T) {
	te := newReplanTestEngine(t, map[string]domain.Issue{
		"9": {ID: "9"},
	})
	te.fake.ProgramResult("9", agent.AgentResult{Status: agent.StatusImplemented})

	ctx := context.Background()
	if err := te.store.FreezeFeature(ctx, replanFeatureID, "plan invalidated", "7"); err != nil {
		t.Fatalf("FreezeFeature: %v", err)
	}

	result, err := te.eng.Execute(ctx, "9", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateCommitting {
		t.Fatalf("final state = %s, want COMMITTING", result.Issue.State)
	}
}

// TestExecute_FrozenMidFlight_CommitsButDoesNotIntegrate is acceptance item
// 4: an in-flight Worker finishes its current step (commit + push of its own
// branch) but is refused at the point that would integrate against the
// invalidated plan — no pull request is created and the Issue parks in
// NEEDS_REPLAN.
func TestExecute_FrozenMidFlight_CommitsButDoesNotIntegrate(t *testing.T) {
	te := newReplanTestEngine(t, map[string]domain.Issue{
		"10": {ID: "10", Title: "Add widget support", Body: materializedBody("plan-rev-1")},
	})
	ctx := context.Background()
	// Another Worker escalates and freezes the Feature while this one is
	// mid-flight: the freeze lands after this Issue was admitted, which is
	// exactly the in-flight case the suspension boundary exists for.
	te.eng.Agent = &freezingAgent{
		freeze: func() error { return te.store.FreezeFeature(ctx, replanFeatureID, "plan invalidated", "7") },
	}
	reviewer := review.NewFakeReviewer()
	reviewer.ProgramResult("10", review.Result{Verdict: review.VerdictApproved, Summary: "ship it"})
	te.eng.Reviewer = reviewer
	te.eng.Diff = &stubDiff{diff: "diff --git a/foo b/foo"}
	pub := &fakePublisher{commitSHA: "abc123"}
	prTracker := newFakePRTracker()
	te.eng.Publisher = pub
	te.eng.PRTracker = prTracker
	te.eng.BaseBranch = "main"

	result, err := te.eng.Execute(ctx, "10", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateNeedsReplan {
		t.Fatalf("final state = %s, want NEEDS_REPLAN", result.Issue.State)
	}

	// The safe suspension boundary: committing and pushing its own branch
	// happened; integrating did not.
	if len(pub.commitCalls) != 1 {
		t.Errorf("commit calls = %d, want 1 (the worker may finish its current step)", len(pub.commitCalls))
	}
	if pub.pushCallCount() != 1 {
		t.Errorf("push calls = %d, want 1", pub.pushCallCount())
	}
	if prTracker.callCount() != 0 {
		t.Errorf("pull request calls = %d, want 0 (must not integrate against the invalidated plan)", prTracker.callCount())
	}
	prs, err := te.store.PullRequestsByIssue(ctx, result.ExecutionID, "10")
	if err != nil {
		t.Fatalf("PullRequestsByIssue: %v", err)
	}
	if len(prs) != 0 {
		t.Errorf("persisted pull requests = %v, want none", prs)
	}

	types := eventTypes(t, te.store, result.ExecutionID)
	if indexOf(types, "replan.integration_blocked") < 0 {
		t.Errorf("no replan.integration_blocked event recorded: %v", types)
	}
}

// TestResumeAfterReplan_RequiresUnfreezeThenRevalidates is acceptance item
// 4's post-approval half and item 5's last clause: frozen work does not
// resume until the Feature is unfrozen by a new plan approval, and when it
// does the suspended result is revalidated rather than trusted.
func TestResumeAfterReplan_RequiresUnfreezeThenRevalidates(t *testing.T) {
	te := newReplanTestEngine(t, map[string]domain.Issue{
		"7": {ID: "7", Body: materializedBody("plan-rev-1")},
	})
	te.fake.ProgramResult("7", replanResult())

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "7", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateNeedsReplan {
		t.Fatalf("state = %s, want NEEDS_REPLAN", result.Issue.State)
	}

	// While frozen, resumption is refused.
	if _, err := te.eng.ResumeAfterReplan(ctx, result.ExecutionID, "7"); err == nil {
		t.Fatal("expected ResumeAfterReplan to refuse a still-frozen Feature")
	} else {
		var frozenErr *engine.FeatureFrozenError
		if !errors.As(err, &frozenErr) {
			t.Fatalf("error = %T (%v), want *engine.FeatureFrozenError", err, err)
		}
	}

	// A new plan approval lifts the freeze.
	if err := te.store.UnfreezeFeature(ctx, replanFeatureID); err != nil {
		t.Fatalf("UnfreezeFeature: %v", err)
	}

	issue, err := te.eng.ResumeAfterReplan(ctx, result.ExecutionID, "7")
	if err != nil {
		t.Fatalf("ResumeAfterReplan: %v", err)
	}
	if issue.State != domain.StateReady {
		t.Fatalf("state = %s, want READY", issue.State)
	}

	events, err := te.store.EventsByIssue(ctx, result.ExecutionID, "7")
	if err != nil {
		t.Fatalf("EventsByIssue: %v", err)
	}
	var sawRevalidated bool
	for _, e := range events {
		if e.Type != "replan.revalidated" {
			continue
		}
		sawRevalidated = true
		var data map[string]string
		if err := json.Unmarshal([]byte(e.Data), &data); err != nil {
			t.Fatalf("unmarshal replan.revalidated: %v", err)
		}
		if data["feature_id"] != replanFeatureID {
			t.Errorf("replan.revalidated data = %v", data)
		}
	}
	if !sawRevalidated {
		t.Error("resuming did not record a revalidation outcome")
	}
}

// TestHandleReplanRequired_RejectsMalformedEscalations pins the two
// structural preconditions: a REPLAN_REQUIRED result must carry a detail
// with a reason, and the Issue must belong to a Feature there is something
// to freeze.
func TestHandleReplanRequired_RejectsMalformedEscalations(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		result agent.AgentResult
		want   string
	}{
		{
			name:   "no replan detail",
			body:   materializedBody("plan-rev-1"),
			result: agent.AgentResult{Status: agent.StatusReplanRequired},
			want:   "no Replan detail",
		},
		{
			name: "blank reason",
			body: materializedBody("plan-rev-1"),
			result: agent.AgentResult{
				Status: agent.StatusReplanRequired,
				Replan: &agent.ReplanDetail{Reason: "  "},
			},
			want: "blank reason",
		},
		{
			name:   "no forge provenance",
			body:   "### Objective\ndo the thing\n",
			result: replanResult(),
			want:   "belongs to no Feature",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			te := newReplanTestEngine(t, map[string]domain.Issue{
				"7": {ID: "7", Body: tc.body},
			})
			te.fake.ProgramResult("7", tc.result)

			_, err := te.eng.Execute(context.Background(), "7", te.base)
			if err == nil {
				t.Fatal("expected the malformed escalation to be rejected")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestReplanNeverRollsBackCompletedWork is acceptance item 3's negative
// assertion at the engine level: an Issue already DONE for the same Feature
// is untouched by another Issue's escalation.
func TestReplanNeverRollsBackCompletedWork(t *testing.T) {
	te := newReplanTestEngine(t, map[string]domain.Issue{
		"7": {ID: "7", Body: materializedBody("plan-rev-1")},
	})
	te.fake.ProgramResult("7", replanResult())

	ctx := context.Background()
	execution, err := te.eng.StartExecution(ctx, te.base)
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}
	done := domain.Issue{
		ExecutionID: execution.ID,
		ID:          "6",
		Title:       "already shipped",
		Body:        materializedBody("plan-rev-1", "REQ-1"),
		State:       domain.StatePending,
		Scope:       domain.ScopeManaged,
		RetryBudget: domain.NewRetryBudget(config.Default().Retry),
	}
	if err := te.store.CreateIssue(ctx, done); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	for _, to := range []domain.IssueState{
		domain.StateReady, domain.StateClaimed, domain.StatePreparing, domain.StateImplementing,
		domain.StateValidating, domain.StateReviewing, domain.StateCommitting,
		domain.StatePRCreating, domain.StateCIPending, domain.StateDone,
	} {
		if _, err := te.store.TransitionIssue(ctx, execution.ID, "6", to); err != nil {
			t.Fatalf("TransitionIssue to %s: %v", to, err)
		}
	}

	if _, err := te.eng.ExecuteInExecution(ctx, execution, "7", te.base); err != nil {
		t.Fatalf("ExecuteInExecution: %v", err)
	}

	completed, err := te.store.GetIssue(ctx, execution.ID, "6")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if completed.State != domain.StateDone {
		t.Fatalf("completed issue state = %s, want DONE (completed work is never rolled back)", completed.State)
	}
}

// TestResumeExecution_NeedsReplanStaysParkedUntilUnfrozen wires acceptance
// items 4 and 5 through `forge resume`'s recovery path: a replan-parked
// Issue makes no progress while its Feature is frozen, and resumes (through
// revalidation) once a new plan approval has lifted the freeze.
func TestResumeExecution_NeedsReplanStaysParkedUntilUnfrozen(t *testing.T) {
	te := newReplanTestEngine(t, map[string]domain.Issue{
		"7": {ID: "7", Body: materializedBody("plan-rev-1")},
	})
	// Both outcomes are queued up front: the OutcomeQueue (internal/fake)
	// repeats a key's last remaining outcome indefinitely once its queue is
	// down to one entry, so reprogramming "7" *after* the first Next call
	// has already collapsed it to a single-item repeat would only stack a
	// second outcome behind the stale repeated one instead of replacing it.
	te.fake.ProgramResult("7", replanResult())
	te.fake.ProgramResult("7", agent.AgentResult{Status: agent.StatusImplemented})

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "7", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	state, err := te.eng.ResumeExecution(ctx, result.ExecutionID)
	if err != nil {
		t.Fatalf("ResumeExecution while frozen: %v", err)
	}
	if len(state.Issues) != 1 || state.Issues[0].State != domain.StateNeedsReplan {
		t.Fatalf("issues = %+v, want the issue still parked in NEEDS_REPLAN", state.Issues)
	}
	if _, err := te.store.WorkerClaim(ctx, result.ExecutionID, "7"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("quiesced work must hold no worker claim, got %v", err)
	}

	// A new plan approval lifts the freeze; the next resume revalidates and
	// re-drives the Issue.
	if err := te.store.UnfreezeFeature(ctx, replanFeatureID); err != nil {
		t.Fatalf("UnfreezeFeature: %v", err)
	}

	state, err = te.eng.ResumeExecution(ctx, result.ExecutionID)
	if err != nil {
		t.Fatalf("ResumeExecution after unfreeze: %v", err)
	}
	if len(state.Issues) != 1 || state.Issues[0].State == domain.StateNeedsReplan {
		t.Fatalf("issues = %+v, want the issue to have left NEEDS_REPLAN", state.Issues)
	}

	types := eventTypes(t, te.store, result.ExecutionID)
	if indexOf(types, "replan.revalidated") < 0 {
		t.Errorf("resuming did not revalidate the suspended result: %v", types)
	}
}
