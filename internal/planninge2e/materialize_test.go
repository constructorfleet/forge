package planninge2e_test

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/decisiongraph"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/materialize"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningagent"
	"github.com/Teagan42/forge/internal/planningsurvey"
	"github.com/Teagan42/forge/internal/specengine"
	"github.com/Teagan42/forge/internal/ticketplan"
	"github.com/Teagan42/forge/internal/tracker"
	"github.com/Teagan42/forge/internal/wayfinding"
)

const pipelineFeatureID = "widget"

// pipeline is one Feature carried all the way from goal to materialized
// tracker Issues.
type pipeline struct {
	goal     *planning.Artifact
	loader   *memLoader
	trk      *featureTracker
	tickets  []ticketplan.Ticket
	opts     materialize.Options
	issueIDs map[string]string
}

// runFullPipeline drives the whole planning compiler for one Feature —
// survey, decision loop, readiness review, spec generation + review, human
// spec approval, ticket plan generation + review, human plan approval — and
// then materializes the approved plan onto a fresh in-memory tracker,
// applying the same gates cmd/forge/materialize.go applies.
func runFullPipeline(t *testing.T, ctx context.Context) pipeline {
	t.Helper()

	goal := newGoal(goalBody)
	loader := newMemLoader(goal)

	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("planning-survey", fenced(`{"decisions":[
		{"temp_key":"store","title":"Storage engine","question":"Which storage engine?",
		 "depends_on":[],"consequential":true}
	]}`))
	backend.ProgramResult("decision-resolution", fenced(`{"outcome":"SQLite","rationale":"single node",
		"consequences":"no clustering","assumptions":"one writer","new_unknowns":[]}`))
	backend.ProgramResult("planning-readiness-review", fenced(`{"status":"READY_FOR_SPEC","decisions":[]}`))
	programApprovedSpec(backend)
	backend.ProgramResult("ticket-plan-generation", fenced(`{"tickets":[
		{"key":"TKT-001","objective":"Persist widgets","requirements":["REQ-001"],
		 "acceptance_criteria":["widgets survive a restart"],"dependencies":[]},
		{"key":"TKT-002","objective":"List widgets","requirements":["REQ-002"],
		 "acceptance_criteria":["listing returns every widget"],"dependencies":["TKT-001"]}
	]}`))
	backend.ProgramResult("ticket-plan-review", fenced(`{"verdict":"APPROVED","summary":"good","findings":[]}`))

	surveyPC, err := planningagent.Compile(repoCtx, []planningagent.NamedArtifact{{ID: "goal", Artifact: goal}}, nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	survey, err := planningsurvey.Propose(ctx, backend, surveyPC)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	goalRef := decisiongraph.GoalRef{ID: "goal", Revision: goal.Revision}
	materialized, err := decisiongraph.Materialize(survey.Decisions, goalRef, nil)
	if err != nil {
		t.Fatalf("decisiongraph.Materialize: %v", err)
	}
	decisions := map[string]*planning.Artifact{}
	for _, m := range materialized {
		decisions[m.ID] = m.Artifact
		if err := loader.persist(m.ID, m.Artifact); err != nil {
			t.Fatalf("persist %s: %v", m.ID, err)
		}
	}
	if err := wayfinding.Loop(ctx, backend, repoCtx, goal, goalRef, decisions, loader.persist, nil); err != nil {
		t.Fatalf("wayfinding.Loop: %v", err)
	}

	eng := specengine.NewSpecEngine(backend)
	if err := eng.GenerateSpec(ctx, pipelineFeatureID, loader); err != nil {
		t.Fatalf("GenerateSpec: %v", err)
	}
	approveArtifact(loader.spec)
	if err := eng.GenerateTicketPlan(ctx, pipelineFeatureID, loader); err != nil {
		t.Fatalf("GenerateTicketPlan: %v", err)
	}
	approveArtifact(loader.ticketPlan)

	// The materialization gate (cmd/forge/materialize.go): both artifacts
	// must be approved at their current revision.
	if !planning.Approved(loader.spec) || !planning.Approved(loader.ticketPlan) {
		t.Fatal("the materialization gate is closed: spec and/or ticket plan are not approved")
	}

	tickets, err := ticketplan.ParseTicketPlan(loader.ticketPlan)
	if err != nil {
		t.Fatalf("ParseTicketPlan: %v", err)
	}

	opts := materialize.Options{
		Project:      pipelineFeatureID,
		SpecRevision: loader.spec.Revision,
		PlanRevision: loader.ticketPlan.Revision,
		Decisions:    specDecisionIDs(loader.spec),
	}

	trk := newFeatureTracker()
	result, err := materialize.Materialize(ctx, trk, tickets, opts)
	if err != nil {
		t.Fatalf("materialize.Materialize: %v", err)
	}

	return pipeline{goal: goal, loader: loader, trk: trk, tickets: tickets, opts: opts, issueIDs: result.IssueIDs}
}

// specDecisionIDs mirrors cmd/forge's relevantDecisions: the Decision IDs a
// Specification was derived from, stamped onto every materialized Issue.
func specDecisionIDs(a *planning.Artifact) []string {
	var ids []string
	for _, d := range a.DerivedFrom {
		if d.Kind == planning.KindDecision {
			ids = append(ids, d.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

// TestScenario11_SuccessfulIssueMaterialization carries an approved
// TicketPlan across the Phase 2 / Phase 1 boundary: every ticket becomes a
// tracker Issue, temporary TKT keys are rewritten to real tracker IDs in the
// canonical Dependencies block, and every Issue carries a `ready` Forge
// Provenance stamp naming the project, spec/plan revisions, requirements,
// and Decisions it came from.
func TestScenario11_SuccessfulIssueMaterialization(t *testing.T) {
	ctx := context.Background()
	p := runFullPipeline(t, ctx)

	if got := sortedKeys(p.issueIDs); !equalStrings(got, []string{"TKT-001", "TKT-002"}) {
		t.Fatalf("materialized keys = %v, want both tickets", got)
	}
	first, second := p.issueIDs["TKT-001"], p.issueIDs["TKT-002"]
	if first == "" || second == "" || first == second {
		t.Fatalf("issue IDs = %v, want two distinct tracker IDs", p.issueIDs)
	}

	// Title and objective survive the crossing.
	issue, err := p.trk.GetIssue(ctx, first)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.Title != "TKT-001: Persist widgets" {
		t.Errorf("issue title = %q", issue.Title)
	}
	mustContain(t, "issue body", issue.Body, "### Objective\nPersist widgets")

	// The independent ticket has an explicit "no dependencies" block; the
	// dependent one names the real tracker ID, not the temp key.
	mustContain(t, "TKT-001 body", issueBody(t, p.trk, first), "## Dependencies: None")
	depBody := issueBody(t, p.trk, second)
	mustContain(t, "TKT-002 body", depBody, "- #"+first)
	if deps, err := tracker.ParseDependencyBlock(depBody); err != nil {
		t.Fatalf("ParseDependencyBlock: %v", err)
	} else if !equalStrings(deps, []string{first}) {
		t.Errorf("TKT-002 dependencies = %v, want [%s]", deps, first)
	}

	// Provenance is stamped ready — the only status Phase 1 will execute —
	// and carries everything Phase 1 needs without reading the planning tree.
	for key, id := range p.issueIDs {
		prov, err := tracker.ParseForgeProvenance(issueBody(t, p.trk, id))
		if err != nil {
			t.Fatalf("ParseForgeProvenance(%s): %v", key, err)
		}
		if prov == nil {
			t.Fatalf("issue %s (%s) has no Forge Provenance block", id, key)
		}
		if prov.Status != tracker.ProvenanceReady {
			t.Errorf("issue %s status = %q, want %q", id, prov.Status, tracker.ProvenanceReady)
		}
		if prov.TempKey != key {
			t.Errorf("issue %s temp_key = %q, want %q", id, prov.TempKey, key)
		}
		if prov.Project != pipelineFeatureID {
			t.Errorf("issue %s project = %q, want %q", id, prov.Project, pipelineFeatureID)
		}
		if prov.SpecRevision != p.opts.SpecRevision || prov.PlanRevision != p.opts.PlanRevision {
			t.Errorf("issue %s revisions = (%q, %q), want (%q, %q)",
				id, prov.SpecRevision, prov.PlanRevision, p.opts.SpecRevision, p.opts.PlanRevision)
		}
		if !equalStrings(prov.Decisions, []string{"001-storage-engine"}) {
			t.Errorf("issue %s decisions = %v, want [001-storage-engine]", id, prov.Decisions)
		}
		if err := tracker.ValidateExecutable(id, issueBody(t, p.trk, id)); err != nil {
			t.Errorf("materialized issue %s is not executable: %v", id, err)
		}
	}
	if prov, _ := tracker.ParseForgeProvenance(issueBody(t, p.trk, first)); prov != nil &&
		!equalStrings(prov.Requirements, []string{"REQ-001"}) {
		t.Errorf("TKT-001 requirements = %v, want [REQ-001]", prov.Requirements)
	}
}

// TestScenario12_GeneratedDAGAcceptedByPhase1 hands the materialized graph
// to Phase 1: the dependency edges form a valid DAG, Phase 1's handoff gate
// admits a ready Issue and drives it through a real Execution, and the same
// gate refuses an Issue left mid-materialization.
func TestScenario12_GeneratedDAGAcceptedByPhase1(t *testing.T) {
	ctx := context.Background()
	p := runFullPipeline(t, ctx)
	first, second := p.issueIDs["TKT-001"], p.issueIDs["TKT-002"]

	// The materialized graph is a well-formed, acyclic Phase 1 DAG whose
	// edges are exactly the ones the TicketPlan described.
	issues := make([]domain.Issue, 0, len(p.issueIDs))
	for _, id := range []string{first, second} {
		body := issueBody(t, p.trk, id)
		deps, err := tracker.ParseDependencyBlock(body)
		if err != nil {
			t.Fatalf("ParseDependencyBlock(%s): %v", id, err)
		}
		issue := domain.Issue{ID: id, Body: body}
		for _, dep := range deps {
			issue.Dependencies = append(issue.Dependencies, domain.Dependency{IssueID: id, DependsOnID: dep})
		}
		issues = append(issues, issue)
	}
	dag, err := tracker.BuildDAG(issues)
	if err != nil {
		t.Fatalf("tracker.BuildDAG rejected the materialized graph: %v", err)
	}
	if !dag.HasNode(first) || !dag.HasNode(second) {
		t.Fatalf("DAG is missing a materialized issue: %v", p.issueIDs)
	}
	if got := dag.DependsOn(second); !equalStrings(got, []string{first}) {
		t.Errorf("DAG edge for %s = %v, want [%s]", second, got, first)
	}
	if got := dag.DependsOn(first); len(got) != 0 {
		t.Errorf("DAG edge for %s = %v, want none", first, got)
	}

	// Phase 1 executes the dependency-free Issue end to end.
	ph1 := newPhase1(t, p.trk)
	ph1.agent.ProgramResult(first, agent.AgentResult{Status: agent.StatusImplemented, Summary: "persisted widgets"})
	result, err := ph1.eng.Execute(ctx, first, ph1.base)
	if err != nil {
		t.Fatalf("Phase 1 Execute of a materialized issue: %v", err)
	}
	if result.Issue.State != domain.StateCommitting {
		t.Fatalf("final state = %s, want COMMITTING", result.Issue.State)
	}

	// The same gate refuses an Issue that never reached Phase C: a partial
	// materialization can never leak an executable Issue.
	stuck := "### Objective\nhalf-materialized\n\n" + tracker.RenderForgeProvenance(tracker.ForgeProvenance{
		Status:       tracker.ProvenanceMaterializing,
		TempKey:      "TKT-009",
		Project:      pipelineFeatureID,
		SpecRevision: p.opts.SpecRevision,
		PlanRevision: p.opts.PlanRevision,
	})
	p.trk.AddIssue(domain.Issue{ID: "999", Body: stuck})
	_, err = ph1.eng.Execute(ctx, "999", ph1.base)
	if err == nil {
		t.Fatal("Phase 1 executed an Issue still stamped materializing")
	}
	var notExecutable *tracker.NotExecutableError
	if !errors.As(err, &notExecutable) {
		t.Fatalf("error %v is not a *tracker.NotExecutableError", err)
	}
	if notExecutable.IssueID != "999" {
		t.Errorf("NotExecutableError.IssueID = %q, want 999", notExecutable.IssueID)
	}
}
