package decisiongraph_test

import (
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/decisiongraph"
	"github.com/Teagan42/forge/internal/planning"
)

func testTrigger() decisiongraph.ReplanTrigger {
	return decisiongraph.ReplanTrigger{
		IssueID:              "42",
		Reason:               "the plan assumes a synchronous API that does not exist",
		Evidence:             "internal/api/client.go exposes only a streaming interface",
		AffectedRequirements: []string{"REQ-3", "REQ-4"},
		SuggestedQuestion:    "should the feature adopt the streaming interface?",
		PlanRevision:         "plan-rev-1",
	}
}

func TestMaterializeReplanTriggerCreatesOpenDecision(t *testing.T) {
	got, err := decisiongraph.MaterializeReplanTrigger(
		testTrigger(),
		decisiongraph.GoalRef{ID: "goal", Revision: "goal-rev"},
		[]string{"001-auth-strategy", "002-storage"},
	)
	if err != nil {
		t.Fatalf("MaterializeReplanTrigger: %v", err)
	}

	if !strings.HasPrefix(got.ID, "003-") {
		t.Errorf("ID = %q, want the next free NNN after 002", got.ID)
	}
	if got.Artifact.Kind != planning.KindDecision {
		t.Errorf("Kind = %q", got.Artifact.Kind)
	}
	if got.Artifact.State != decisiongraph.StateReplanRequired {
		t.Errorf("State = %q, want %q", got.Artifact.State, decisiongraph.StateReplanRequired)
	}
	if got.Artifact.ApprovedRevision != "" {
		t.Error("a freshly materialized replan Decision must not be approved")
	}
	if planning.Approved(got.Artifact) || planning.Ready(got.Artifact) {
		t.Error("replan Decision must evaluate unapproved and not ready")
	}
	if planning.Stale(got.Artifact) {
		t.Error("materialized Decision must record its own current revision")
	}

	body := sectionBodyOf(t, got.Artifact, "Replan Trigger")
	for _, want := range []string{
		"Reported by issue: 42",
		"Ticket plan revision: plan-rev-1",
		"the plan assumes a synchronous API that does not exist",
		"internal/api/client.go exposes only a streaming interface",
		"REQ-3, REQ-4",
		"should the feature adopt the streaming interface?",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Replan Trigger section missing %q:\n%s", want, body)
		}
	}

	question := sectionBodyOf(t, got.Artifact, "Question")
	if question != "should the feature adopt the streaming interface?" {
		t.Errorf("Question = %q, want the suggested question", question)
	}
}

func TestMaterializeReplanTriggerRejectsBlankReason(t *testing.T) {
	trigger := testTrigger()
	trigger.Reason = "   "
	if _, err := decisiongraph.MaterializeReplanTrigger(trigger, decisiongraph.GoalRef{ID: "goal"}, nil); err == nil {
		t.Fatal("expected a blank reason to be rejected")
	}
}

// TestReopenMakesDownstreamArtifactsStaleThroughProvenance is acceptance
// item 2's staleness requirement: reopening the Decision moves its content
// revision, so an artifact that recorded the old revision in its
// DerivedFrom no longer matches it. Nothing flips a stale bit.
func TestReopenMakesDownstreamArtifactsStaleThroughProvenance(t *testing.T) {
	decision := &planning.Artifact{
		Kind:  planning.KindDecision,
		State: "resolved",
		DerivedFrom: []planning.DerivedFromEntry{
			{Kind: planning.KindGoal, ID: "goal", Revision: "goal-rev"},
		},
		Sections: []planning.Section{
			{Heading: "Question", Body: "which transport?"},
			{Heading: "Outcome", Body: "synchronous HTTP"},
		},
	}
	decision.Revision = planning.ComputeRevision(decision)
	decision.ApprovedRevision = decision.Revision

	if !planning.Approved(decision) {
		t.Fatal("test setup: decision should start approved")
	}

	// A downstream artifact derived from the approved Decision revision.
	spec := &planning.Artifact{
		Kind: planning.KindSpec,
		DerivedFrom: []planning.DerivedFromEntry{
			{Kind: planning.KindDecision, ID: "001-transport", Revision: decision.Revision},
		},
		Sections: []planning.Section{{Heading: "Context", Body: "sync transport"}},
	}
	spec.Revision = planning.ComputeRevision(spec)

	reopened := decisiongraph.Reopen(decision, testTrigger())

	if reopened.Revision == decision.Revision {
		t.Fatal("reopening must move the Decision's content revision")
	}
	if reopened.ApprovedRevision != "" {
		t.Error("reopened Decision must not carry an approval")
	}
	if planning.Approved(reopened) || planning.Ready(reopened) {
		t.Error("reopened Decision must evaluate unapproved and not ready")
	}
	if planning.Stale(reopened) {
		t.Error("Reopen must record the recomputed revision on the artifact it returns")
	}
	if reopened.State != decisiongraph.StateReplanRequired {
		t.Errorf("State = %q, want %q", reopened.State, decisiongraph.StateReplanRequired)
	}

	// The downstream spec is unchanged on disk, yet its recorded provenance
	// no longer matches the Decision it claims to derive from.
	if planning.Stale(spec) {
		t.Fatal("the spec's own content did not change; it must not be self-stale")
	}
	if spec.DerivedFrom[0].Revision == reopened.Revision {
		t.Fatal("spec provenance still matches the reopened Decision: it would not evaluate stale")
	}

	// Prior reasoning survives: completed work stays explicable.
	if got := sectionBodyOf(t, reopened, "Outcome"); got != "synchronous HTTP" {
		t.Errorf("Outcome = %q, want the original outcome preserved", got)
	}
	if got := sectionBodyOf(t, reopened, "Question"); got != "which transport?" {
		t.Errorf("Question = %q, want the original question preserved", got)
	}
}

func TestReopenIsDeterministicAndAccumulatesTriggers(t *testing.T) {
	decision := &planning.Artifact{
		Kind:     planning.KindDecision,
		State:    "resolved",
		Sections: []planning.Section{{Heading: "Question", Body: "which transport?"}},
	}
	decision.Revision = planning.ComputeRevision(decision)

	first := decisiongraph.Reopen(decision, testTrigger())
	again := decisiongraph.Reopen(decision, testTrigger())
	if first.Revision != again.Revision {
		t.Error("Reopen must be deterministic for the same input")
	}

	second := testTrigger()
	second.IssueID = "43"
	second.Reason = "a second worker found the same plan invalid elsewhere"
	stacked := decisiongraph.Reopen(first, second)

	body := sectionBodyOf(t, stacked, "Replan Trigger")
	if !strings.Contains(body, "Reported by issue: 42") {
		t.Errorf("second reopen dropped the first trigger:\n%s", body)
	}
	if !strings.Contains(body, "Reported by issue: 43") {
		t.Errorf("second reopen did not record the second trigger:\n%s", body)
	}
	if stacked.Revision == first.Revision {
		t.Error("a second trigger must move the revision again")
	}
}

func TestFindReplanDecisionMatchesReportingIssue(t *testing.T) {
	materialized, err := decisiongraph.MaterializeReplanTrigger(testTrigger(), decisiongraph.GoalRef{ID: "goal"}, nil)
	if err != nil {
		t.Fatalf("MaterializeReplanTrigger: %v", err)
	}
	decisions := map[string]*planning.Artifact{
		"001-unrelated": {Kind: planning.KindDecision, Sections: []planning.Section{{Heading: "Question", Body: "q"}}},
		materialized.ID: materialized.Artifact,
	}

	got, ok := decisiongraph.FindReplanDecision(decisions, "42")
	if !ok || got != materialized.ID {
		t.Fatalf("FindReplanDecision = (%q, %v), want (%q, true)", got, ok, materialized.ID)
	}

	if _, ok := decisiongraph.FindReplanDecision(decisions, "99"); ok {
		t.Error("an unrelated issue must not match an existing replan Decision")
	}
	if _, ok := decisiongraph.FindReplanDecision(decisions, ""); ok {
		t.Error("a blank issue ID must never match")
	}
}

func sectionBodyOf(t *testing.T, a *planning.Artifact, heading string) string {
	t.Helper()
	for _, s := range a.Sections {
		if s.Heading == heading {
			return s.Body
		}
	}
	t.Fatalf("artifact has no %q section: %+v", heading, a.Sections)
	return ""
}
