package planninge2e_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/decisiongraph"
	"github.com/Teagan42/forge/internal/decisionresolution"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/planengine"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningagent"
	"github.com/Teagan42/forge/internal/replan"
	"github.com/Teagan42/forge/internal/spec"
	"github.com/Teagan42/forge/internal/specengine"
)

// TestScenario13_ReplanRequiredInvalidatesDownstreamArtifacts follows a
// REPLAN_REQUIRED escalation all the way back through the compiler: the
// Worker's Issue is parked, the Feature is frozen before the planning lease
// is taken, the trigger is materialized as an unapproved Decision back on
// the frontier, no new work may start, and — once that Decision is resolved,
// a new Spec derived from it, and the same Issue escalates again — reopening
// the Decision invalidates every downstream artifact through provenance
// alone.
func TestScenario13_ReplanRequiredInvalidatesDownstreamArtifacts(t *testing.T) {
	ctx := context.Background()
	p := runFullPipeline(t, ctx)
	first, second := p.issueIDs["TKT-001"], p.issueIDs["TKT-002"]

	ph1 := newPhase1(t, p.trk)
	ph1.eng.NeedsInfoTracker = p.trk
	ph1.eng.PlanningLease = planengine.New(ph1.store)
	ph1.eng.ReplanDecisions = replan.DecisionRecorder{Decisions: p.loader}

	ph1.agent.ProgramResult(first, agent.AgentResult{
		Status:  agent.StatusReplanRequired,
		Summary: "the plan cannot be implemented as written",
		Replan: &agent.ReplanDetail{
			Reason:               "storage cannot satisfy the listing requirement",
			Evidence:             "SQLite table has no ordering column",
			AffectedRequirements: []string{"REQ-002"},
			SuggestedQuestion:    "should listing be served from a separate index?",
		},
	})

	result, err := ph1.eng.Execute(ctx, first, ph1.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateNeedsReplan {
		t.Fatalf("issue state = %s, want NEEDS_REPLAN", result.Issue.State)
	}

	// The Feature is frozen, and the freeze names the escalating Issue.
	frozen, freeze, err := ph1.store.IsFeatureFrozen(ctx, pipelineFeatureID)
	if err != nil {
		t.Fatalf("IsFeatureFrozen: %v", err)
	}
	if !frozen {
		t.Fatal("REPLAN_REQUIRED did not freeze the feature")
	}
	if freeze.TriggeringIssueID != first {
		t.Errorf("freeze.TriggeringIssueID = %q, want %q", freeze.TriggeringIssueID, first)
	}

	// The planning lease was taken for the Feature (freeze strictly first —
	// see engine.handleReplanRequired).
	lease, err := ph1.store.FeaturePlanningLease(ctx, pipelineFeatureID)
	if err != nil {
		t.Fatalf("FeaturePlanningLease: %v", err)
	}
	if lease.ExecutionID == "" {
		t.Error("no planning execution owns the feature planning lease")
	}

	// The trigger is materialized as a Decision: open, unapproved, back on
	// the frontier, carrying the Worker's evidence.
	decisions, err := p.loader.LoadDecisions(ctx, pipelineFeatureID)
	if err != nil {
		t.Fatalf("LoadDecisions: %v", err)
	}
	replanID := ""
	for id, d := range decisions {
		if d.State == decisiongraph.StateReplanRequired {
			replanID = id
		}
	}
	if replanID == "" {
		t.Fatalf("no decision was reopened or created for the replan: %v", sortedKeys(decisions))
	}
	if !strings.HasPrefix(replanID, "002-replan-") {
		t.Errorf("replan decision ID = %q, want a 002-replan-* ID from the shared NNN-slug machinery", replanID)
	}
	replanDecision := decisions[replanID]
	if planning.Ready(replanDecision) {
		t.Error("a freshly opened replan decision must not be Ready")
	}
	if replanDecision.ApprovedRevision != "" {
		t.Errorf("replan decision carries an approval stamp %q", replanDecision.ApprovedRevision)
	}
	if got := sectionBody(replanDecision, "Question"); got != "should listing be served from a separate index?" {
		t.Errorf("replan decision Question = %q, want the Worker's suggested question", got)
	}
	trigger := sectionBody(replanDecision, "Replan Trigger")
	mustContain(t, "replan trigger", trigger, "Reported by issue: "+first)
	mustContain(t, "replan trigger", trigger, "Ticket plan revision: "+p.opts.PlanRevision)
	mustContain(t, "replan trigger", trigger, "SQLite table has no ordering column")
	mustContain(t, "replan trigger", trigger, "Affected requirements: REQ-002")
	if got := decisiongraph.Frontier(decisions); !equalStrings(got, []string{replanID}) {
		t.Errorf("frontier = %v, want exactly the replan decision %s", got, replanID)
	}

	// A frozen Feature starts no new work: the sibling Issue is refused
	// before any Issue row or claim exists for it.
	_, err = ph1.eng.Execute(ctx, second, ph1.base)
	if err == nil {
		t.Fatal("a frozen feature admitted new work")
	}
	var frozenErr *engine.FeatureFrozenError
	if !errors.As(err, &frozenErr) {
		t.Fatalf("error %v is not a *engine.FeatureFrozenError", err)
	}
	if frozenErr.FeatureID != pipelineFeatureID || frozenErr.IssueID != second {
		t.Errorf("FeatureFrozenError = %+v", frozenErr)
	}

	// The same Worker escalating again reopens the same Decision in place
	// rather than accumulating near-duplicates, and both triggers stay
	// readable.
	recorder := replan.DecisionRecorder{Decisions: p.loader}
	againID, err := recorder.RecordReplanTrigger(ctx, pipelineFeatureID, first, p.opts.PlanRevision, agent.ReplanDetail{
		Reason:            "the ordering column is still missing after a retry",
		SuggestedQuestion: "should listing be served from a separate index?",
	})
	if err != nil {
		t.Fatalf("RecordReplanTrigger (second escalation): %v", err)
	}
	if againID != replanID {
		t.Fatalf("second escalation from issue %s opened decision %q, want the existing %q", first, againID, replanID)
	}
	reEscalated, err := p.loader.LoadDecisions(ctx, pipelineFeatureID)
	if err != nil {
		t.Fatalf("LoadDecisions: %v", err)
	}
	accumulated := sectionBody(reEscalated[replanID], "Replan Trigger")
	mustContain(t, "accumulated trigger", accumulated, "storage cannot satisfy the listing requirement")
	mustContain(t, "accumulated trigger", accumulated, "the ordering column is still missing after a retry")

	// --- replanning: resolve the new Decision and write a new Spec around it

	resolved := decisiongraph.ApplyResolution(reEscalated[replanID], decisionresolution.Result{
		Outcome:      "serve listing from a dedicated index table",
		Rationale:    "keeps the write path unchanged",
		Consequences: "one more table to migrate",
	})
	if err := p.loader.SaveDecision(ctx, pipelineFeatureID, replanID, resolved); err != nil {
		t.Fatalf("SaveDecision: %v", err)
	}
	if !planning.Ready(resolved) {
		t.Fatal("an applied resolution is not Ready")
	}

	replanBackend := planningagent.NewFakeBackend()
	programApprovedSpec(replanBackend)
	if err := specengine.NewSpecEngine(replanBackend).GenerateSpec(ctx, pipelineFeatureID, p.loader); err != nil {
		t.Fatalf("GenerateSpec after replan: %v", err)
	}
	newSpec := p.loader.spec
	approveArtifact(newSpec)
	recorded, ok := derivedRevision(newSpec, replanID)
	if !ok {
		t.Fatalf("the replanned spec does not derive from %s: %+v", replanID, newSpec.DerivedFrom)
	}
	if recorded != resolved.Revision {
		t.Fatalf("spec derived_from %s revision = %q, want %q", replanID, recorded, resolved.Revision)
	}

	// --- the resolved Decision is reopened by a later escalation

	// decisiongraph.Reopen is the mechanic internal/replan's recorder wraps
	// (its idempotent-reopen half was asserted above, while the Decision was
	// still open); applying it directly here keeps this half focused on what
	// reopening does to everything derived from the Decision.
	reopenedDecision := decisiongraph.Reopen(resolved, decisiongraph.ReplanTrigger{
		IssueID:      first,
		Reason:       "the index table still cannot satisfy ordering",
		PlanRevision: p.opts.PlanRevision,
	})
	if err := p.loader.SaveDecision(ctx, pipelineFeatureID, replanID, reopenedDecision); err != nil {
		t.Fatalf("SaveDecision: %v", err)
	}
	reopened, err := p.loader.LoadDecisions(ctx, pipelineFeatureID)
	if err != nil {
		t.Fatalf("LoadDecisions: %v", err)
	}
	if reopenedDecision.State != decisiongraph.StateReplanRequired {
		t.Errorf("reopened decision state = %q, want %q", reopenedDecision.State, decisiongraph.StateReplanRequired)
	}
	if planning.Ready(reopenedDecision) {
		t.Error("a reopened decision must not be Ready")
	}
	// Its prior reasoning is preserved, and the new trigger is readable.
	if got := sectionBody(reopenedDecision, "Outcome"); got != "serve listing from a dedicated index table" {
		t.Errorf("reopening lost the decision's prior outcome: %q", got)
	}
	mustContain(t, "new trigger", sectionBody(reopenedDecision, "Replan Trigger"),
		"the index table still cannot satisfy ordering")

	// Downstream invalidation, through provenance alone: the approved Spec
	// still matches its own content, but no longer matches the Decision it
	// was derived from.
	if !planning.Approved(newSpec) {
		t.Error("the spec's own approval binds its own content and must survive the reopen")
	}
	if planning.ComputeRevision(reopenedDecision) == recorded {
		t.Fatal("reopening the decision did not move its content revision")
	}
	err = spec.ValidateSpecDeterministic(
		&planning.Artifact{Kind: newSpec.Kind, Sections: newSpec.Sections, DerivedFrom: newSpec.DerivedFrom},
		reopened,
		p.goal.Revision,
		map[string]string{replanID: planning.ComputeRevision(reopenedDecision)},
		mustDerivedRevision(t, newSpec, "repository"),
	)
	if err == nil {
		t.Fatal("ValidateSpecDeterministic accepted a spec derived from a reopened decision")
	}
	if !strings.Contains(err.Error(), "decision "+replanID+" revision mismatch") {
		t.Errorf("validation error = %v, want a revision mismatch naming %s", err, replanID)
	}

	// --- approving a new plan closes the loop: dropped work is superseded
	// before the freeze lifts.

	superseded, err := replan.ResumeFeature(ctx, ph1.store, pipelineFeatureID, "plan-rev-2", []string{"TKT-002"})
	if err != nil {
		t.Fatalf("ResumeFeature: %v", err)
	}
	if !equalStrings(superseded, []string{first}) {
		t.Fatalf("superseded = %v, want the dropped issue [%s]", superseded, first)
	}
	dropped, err := ph1.store.GetIssue(ctx, result.ExecutionID, first)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if dropped.State != domain.StateCancelled {
		t.Errorf("superseded issue state = %s, want CANCELLED", dropped.State)
	}
	events, err := ph1.store.EventsByIssue(ctx, result.ExecutionID, first)
	if err != nil {
		t.Fatalf("EventsByIssue: %v", err)
	}
	sawSuperseded := false
	for _, e := range events {
		if e.Type == "issue.superseded" {
			sawSuperseded = true
		}
	}
	if !sawSuperseded {
		t.Error("no issue.superseded event was recorded")
	}
	stillFrozen, _, err := ph1.store.IsFeatureFrozen(ctx, pipelineFeatureID)
	if err != nil {
		t.Fatalf("IsFeatureFrozen: %v", err)
	}
	if stillFrozen {
		t.Error("the feature is still frozen after a new plan was approved")
	}
}
