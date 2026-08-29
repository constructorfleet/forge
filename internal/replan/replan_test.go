package replan_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/decisiongraph"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/replan"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
)

const featureID = "widget-feature"

func openTestStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "forge.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return store
}

func bodyFor(project, tempKey, planRevision string, requirements ...string) string {
	return "### Objective\ndo the thing\n\n" + tracker.RenderForgeProvenance(tracker.ForgeProvenance{
		Status:       tracker.ProvenanceReady,
		TempKey:      tempKey,
		Project:      project,
		SpecRevision: "spec-rev-1",
		PlanRevision: planRevision,
		Requirements: requirements,
	})
}

// seedIssue creates an Issue in executionID and walks it to state.
func seedIssue(t *testing.T, store *storage.SQLiteStore, executionID, issueID, title, body string, path ...domain.IssueState) {
	t.Helper()
	ctx := context.Background()
	if err := store.CreateIssue(ctx, domain.Issue{
		ExecutionID: executionID,
		ID:          issueID,
		Title:       title,
		Body:        body,
		State:       domain.StatePending,
		Scope:       domain.ScopeManaged,
		RetryBudget: domain.NewRetryBudget(config.Default().Retry),
	}); err != nil {
		t.Fatalf("CreateIssue %s: %v", issueID, err)
	}
	for _, to := range path {
		if _, err := store.TransitionIssue(ctx, executionID, issueID, to); err != nil {
			t.Fatalf("TransitionIssue %s -> %s: %v", issueID, to, err)
		}
	}
}

func seedExecution(t *testing.T, store *storage.SQLiteStore, id string) {
	t.Helper()
	if err := store.CreateExecution(context.Background(), domain.Execution{ID: id, BaseRevision: "base"}); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
}

var toDone = []domain.IssueState{
	domain.StateReady, domain.StateClaimed, domain.StatePreparing, domain.StateImplementing,
	domain.StateValidating, domain.StateReviewing, domain.StateCommitting,
	domain.StatePRCreating, domain.StateCIPending, domain.StateDone,
}

var toNeedsReplan = []domain.IssueState{
	domain.StateReady, domain.StateClaimed, domain.StatePreparing,
	domain.StateImplementing, domain.StateNeedsReplan,
}

// TestGatherImplementedFactsCollectsOnlyDoneWork is acceptance item 3: DONE
// work enters PlanningContext as fact, carrying the *old* ticket plan
// revision it was completed under.
func TestGatherImplementedFactsCollectsOnlyDoneWork(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	seedExecution(t, store, "exec-1")

	seedIssue(t, store, "exec-1", "1", "shipped auth", bodyFor(featureID, "TKT-001", "plan-rev-1", "REQ-1"), toDone...)
	seedIssue(t, store, "exec-1", "2", "still queued", bodyFor(featureID, "TKT-002", "plan-rev-1"))
	seedIssue(t, store, "exec-1", "3", "suspended mid-replan", bodyFor(featureID, "TKT-003", "plan-rev-1"), toNeedsReplan...)
	seedIssue(t, store, "exec-1", "4", "other feature", bodyFor("other-feature", "TKT-001", "plan-rev-9"), toDone...)
	seedIssue(t, store, "exec-1", "5", "no provenance", "### Objective\nhand written\n", toDone...)

	facts, err := replan.GatherImplementedFacts(ctx, store, featureID)
	if err != nil {
		t.Fatalf("GatherImplementedFacts: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("facts = %+v, want exactly the one DONE issue of this feature", facts)
	}
	if facts[0].IssueID != "1" || facts[0].Summary != "shipped auth" {
		t.Errorf("fact = %+v", facts[0])
	}
	if facts[0].PlanRevision != "plan-rev-1" {
		t.Errorf("fact.PlanRevision = %q, want the old plan revision the work was completed under", facts[0].PlanRevision)
	}
	if len(facts[0].Requirements) != 1 || facts[0].Requirements[0] != "REQ-1" {
		t.Errorf("fact.Requirements = %v", facts[0].Requirements)
	}
}

// TestGatherImplementedFactsExcludesWorkSuspendedMidReplan is acceptance
// item 4's readiness carve-out: an Issue that merely finished up to its safe
// suspension boundary is parked in NEEDS_REPLAN and never counts as fact.
func TestGatherImplementedFactsExcludesWorkSuspendedMidReplan(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	seedExecution(t, store, "exec-1")
	seedIssue(t, store, "exec-1", "3", "suspended", bodyFor(featureID, "TKT-003", "plan-rev-1"), toNeedsReplan...)

	facts, err := replan.GatherImplementedFacts(ctx, store, featureID)
	if err != nil {
		t.Fatalf("GatherImplementedFacts: %v", err)
	}
	if len(facts) != 0 {
		t.Fatalf("facts = %+v, want none: finishing mid-replan is not an implemented fact", facts)
	}
}

// TestSupersedeUnstartedClosesOnlyAbsentUnstartedIssues is acceptance item
// 5: unstarted Issues absent from the new plan are closed as superseded;
// completed work and Issues the new plan still contains are untouched.
func TestSupersedeUnstartedClosesOnlyAbsentUnstartedIssues(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	seedExecution(t, store, "exec-1")

	seedIssue(t, store, "exec-1", "1", "shipped", bodyFor(featureID, "TKT-001", "plan-rev-1"), toDone...)
	seedIssue(t, store, "exec-1", "2", "kept by new plan", bodyFor(featureID, "TKT-002", "plan-rev-1"))
	seedIssue(t, store, "exec-1", "3", "dropped by new plan", bodyFor(featureID, "TKT-003", "plan-rev-1"))
	seedIssue(t, store, "exec-1", "4", "suspended and dropped", bodyFor(featureID, "TKT-004", "plan-rev-1"), toNeedsReplan...)
	seedIssue(t, store, "exec-1", "5", "other feature", bodyFor("other-feature", "TKT-003", "plan-rev-9"))

	superseded, err := replan.SupersedeUnstarted(ctx, store, featureID, "plan-rev-2", []string{"TKT-002"})
	if err != nil {
		t.Fatalf("SupersedeUnstarted: %v", err)
	}
	if len(superseded) != 2 || superseded[0] != "3" || superseded[1] != "4" {
		t.Fatalf("superseded = %v, want [3 4]", superseded)
	}

	want := map[string]domain.IssueState{
		"1": domain.StateDone,      // completed work is fact; never rolled back
		"2": domain.StatePending,   // still in the new plan
		"3": domain.StateCancelled, // superseded
		"4": domain.StateCancelled, // superseded
		"5": domain.StatePending,   // different feature
	}
	for id, wantState := range want {
		issue, err := store.GetIssue(ctx, "exec-1", id)
		if err != nil {
			t.Fatalf("GetIssue %s: %v", id, err)
		}
		if issue.State != wantState {
			t.Errorf("issue %s state = %s, want %s", id, issue.State, wantState)
		}
	}

	events, err := store.EventsByIssue(ctx, "exec-1", "3")
	if err != nil {
		t.Fatalf("EventsByIssue: %v", err)
	}
	var sawSuperseded bool
	for _, e := range events {
		if e.Type != "issue.superseded" {
			continue
		}
		sawSuperseded = true
		if !strings.Contains(e.Data, "plan-rev-2") {
			t.Errorf("issue.superseded data does not name the superseding plan revision: %s", e.Data)
		}
		if !strings.Contains(e.Data, "plan-rev-1") {
			t.Errorf("issue.superseded data does not name the previous plan revision: %s", e.Data)
		}
	}
	if !sawSuperseded {
		t.Error("no issue.superseded event was recorded")
	}
}

// TestSupersedeUnstartedMatchesTrackerIDsToo covers the second accepted
// identity: a caller diffing against tracker IDs rather than ticket keys.
func TestSupersedeUnstartedMatchesTrackerIDsToo(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	seedExecution(t, store, "exec-1")
	seedIssue(t, store, "exec-1", "2", "kept", bodyFor(featureID, "TKT-002", "plan-rev-1"))

	superseded, err := replan.SupersedeUnstarted(ctx, store, featureID, "plan-rev-2", []string{"2"})
	if err != nil {
		t.Fatalf("SupersedeUnstarted: %v", err)
	}
	if len(superseded) != 0 {
		t.Fatalf("superseded = %v, want none", superseded)
	}
}

// TestResumeFeatureSupersedesBeforeUnfreezing is acceptance item 5's
// ordering: the freeze is never lifted while an Issue the new plan dropped
// is still schedulable.
func TestResumeFeatureSupersedesBeforeUnfreezing(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	seedExecution(t, store, "exec-1")
	seedIssue(t, store, "exec-1", "3", "dropped", bodyFor(featureID, "TKT-003", "plan-rev-1"))

	if _, err := replan.ResumeFeature(ctx, store, featureID, "plan-rev-2", nil); !errors.Is(err, replan.ErrNotFrozen) {
		t.Fatalf("ResumeFeature on an unfrozen feature = %v, want ErrNotFrozen", err)
	}

	if err := store.FreezeFeature(ctx, featureID, "plan invalid", "7"); err != nil {
		t.Fatalf("FreezeFeature: %v", err)
	}

	superseded, err := replan.ResumeFeature(ctx, store, featureID, "plan-rev-2", []string{"TKT-009"})
	if err != nil {
		t.Fatalf("ResumeFeature: %v", err)
	}
	if len(superseded) != 1 || superseded[0] != "3" {
		t.Fatalf("superseded = %v, want [3]", superseded)
	}

	issue, err := store.GetIssue(ctx, "exec-1", "3")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.State != domain.StateCancelled {
		t.Errorf("issue state = %s, want CANCELLED before the freeze lifts", issue.State)
	}

	frozen, _, err := store.IsFeatureFrozen(ctx, featureID)
	if err != nil {
		t.Fatalf("IsFeatureFrozen: %v", err)
	}
	if frozen {
		t.Error("ResumeFeature did not lift the freeze")
	}
}

// memoryDecisions is an in-memory replan.DecisionStore.
type memoryDecisions struct {
	goal      *planning.Artifact
	decisions map[string]*planning.Artifact
	saves     []string
}

func newMemoryDecisions() *memoryDecisions {
	goal := &planning.Artifact{
		Kind:     planning.KindGoal,
		Sections: []planning.Section{{Heading: "Goal", Body: "ship widgets"}},
	}
	goal.Revision = planning.ComputeRevision(goal)
	return &memoryDecisions{goal: goal, decisions: map[string]*planning.Artifact{}}
}

func (m *memoryDecisions) LoadGoal(context.Context, string) (*planning.Artifact, error) {
	return m.goal, nil
}

func (m *memoryDecisions) LoadDecisions(context.Context, string) (map[string]*planning.Artifact, error) {
	out := make(map[string]*planning.Artifact, len(m.decisions))
	for id, d := range m.decisions {
		out[id] = d
	}
	return out, nil
}

func (m *memoryDecisions) SaveDecision(_ context.Context, _, decisionID string, decision *planning.Artifact) error {
	m.decisions[decisionID] = decision
	m.saves = append(m.saves, decisionID)
	return nil
}

func triggerDetail() agent.ReplanDetail {
	return agent.ReplanDetail{
		Reason:               "the plan assumes a synchronous API that does not exist",
		Evidence:             "client.go only streams",
		AffectedRequirements: []string{"REQ-3"},
		SuggestedQuestion:    "adopt the streaming interface?",
	}
}

// TestDecisionRecorderCreatesThenReopens is acceptance item 2's Decision
// half: the first escalation creates a Decision, and a second escalation
// from the same Issue reopens that same Decision rather than creating a
// near-duplicate.
func TestDecisionRecorderCreatesThenReopens(t *testing.T) {
	ctx := context.Background()
	decisions := newMemoryDecisions()
	recorder := replan.DecisionRecorder{Decisions: decisions}

	id, err := recorder.RecordReplanTrigger(ctx, featureID, "7", "plan-rev-1", triggerDetail())
	if err != nil {
		t.Fatalf("RecordReplanTrigger: %v", err)
	}
	created := decisions.decisions[id]
	if created == nil {
		t.Fatal("no decision was saved")
	}
	if created.State != decisiongraph.StateReplanRequired {
		t.Errorf("State = %q", created.State)
	}
	if planning.Approved(created) {
		t.Error("a replan Decision must evaluate unapproved")
	}
	if created.DerivedFrom[0].Revision != decisions.goal.Revision {
		t.Errorf("new Decision did not record goal provenance: %+v", created.DerivedFrom)
	}

	// A second escalation from the same Issue reopens rather than creating.
	sameID, err := recorder.RecordReplanTrigger(ctx, featureID, "7", "plan-rev-1", triggerDetail())
	if err != nil {
		t.Fatalf("second RecordReplanTrigger: %v", err)
	}
	if sameID != id {
		t.Fatalf("second escalation created decision %q, want the existing %q reopened", sameID, id)
	}
	if len(decisions.decisions) != 1 {
		t.Fatalf("decisions = %v, want exactly one", decisions.saves)
	}

	// A different Issue escalating does create a second Decision.
	otherID, err := recorder.RecordReplanTrigger(ctx, featureID, "8", "plan-rev-1", triggerDetail())
	if err != nil {
		t.Fatalf("third RecordReplanTrigger: %v", err)
	}
	if otherID == id {
		t.Fatal("a different reporting Issue must open its own Decision")
	}
	if len(decisions.decisions) != 2 {
		t.Fatalf("decisions = %v, want two", decisions.saves)
	}
}

// TestDecisionRecorderReopeningApprovedDecisionDropsApproval is the
// provenance half of acceptance item 2, end to end through the recorder.
func TestDecisionRecorderReopeningApprovedDecisionDropsApproval(t *testing.T) {
	ctx := context.Background()
	decisions := newMemoryDecisions()
	recorder := replan.DecisionRecorder{Decisions: decisions}

	id, err := recorder.RecordReplanTrigger(ctx, featureID, "7", "plan-rev-1", triggerDetail())
	if err != nil {
		t.Fatalf("RecordReplanTrigger: %v", err)
	}

	// A human resolves and approves it.
	resolved := decisions.decisions[id]
	resolved.State = "resolved"
	resolved.Sections = append(resolved.Sections, planning.Section{Heading: "Outcome", Body: "adopt streaming"})
	resolved.Revision = planning.ComputeRevision(resolved)
	resolved.ApprovedRevision = resolved.Revision
	approvedRevision := resolved.Revision
	if !planning.Approved(resolved) {
		t.Fatal("test setup: decision should be approved")
	}

	if _, err := recorder.RecordReplanTrigger(ctx, featureID, "7", "plan-rev-2", triggerDetail()); err != nil {
		t.Fatalf("second RecordReplanTrigger: %v", err)
	}

	reopened := decisions.decisions[id]
	if planning.Approved(reopened) {
		t.Error("reopening must drop the approval")
	}
	if reopened.Revision == approvedRevision {
		t.Error("reopening must move the content revision so downstream provenance no longer matches")
	}
	if body := sectionBody(reopened, "Outcome"); body != "adopt streaming" {
		t.Errorf("prior reasoning was lost on reopen: %q", body)
	}
}

func sectionBody(a *planning.Artifact, heading string) string {
	for _, s := range a.Sections {
		if s.Heading == heading {
			return s.Body
		}
	}
	return ""
}
