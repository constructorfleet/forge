package planninge2e_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningagent"
	"github.com/Teagan42/forge/internal/specengine"
	"github.com/Teagan42/forge/internal/ticketplan"
)

// seedApprovedSpec runs the Specification stage for real (generation +
// review) and then applies the human approval `forge approve <feature> spec`
// would, leaving a two-requirement approved Spec in the loader.
func seedApprovedSpec(t *testing.T, ctx context.Context, backend *planningagent.FakeBackend, loader *memLoader) {
	t.Helper()
	programApprovedSpec(backend)
	if err := specengine.NewSpecEngine(backend).GenerateSpec(ctx, "widget", loader); err != nil {
		t.Fatalf("GenerateSpec: %v", err)
	}
	if loader.spec == nil {
		t.Fatal("no spec was saved")
	}
	approveArtifact(loader.spec)
}

// ticketPlanError extracts the typed *ticketplan.TicketPlanError from a
// specengine error chain, failing the test when there is none.
func ticketPlanError(t *testing.T, err error) *ticketplan.TicketPlanError {
	t.Helper()
	var tpErr *ticketplan.TicketPlanError
	if !errors.As(err, &tpErr) {
		t.Fatalf("error %v is not a *ticketplan.TicketPlanError", err)
	}
	return tpErr
}

// TestScenario07_TicketPlanCycleRejected covers deterministic rejection of a
// cyclic TicketPlan: the generation contract itself only forbids
// self-dependencies, so a two-node cycle reaches
// ticketplan.ValidateTicketPlanDeterministic, which rejects it with
// CYCLIC_DEPENDENCY before any review runs and before anything is saved.
func TestScenario07_TicketPlanCycleRejected(t *testing.T) {
	ctx := context.Background()
	goal := newGoal(goalBody)
	loader := newMemLoader(goal)
	loader.decisions = storageDecision(goal)

	backend := planningagent.NewFakeBackend()
	seedApprovedSpec(t, ctx, backend, loader)

	backend.ProgramResult("ticket-plan-generation", fenced(`{"tickets":[
		{"key":"TKT-001","objective":"Persist widgets","requirements":["REQ-001"],
		 "acceptance_criteria":["widgets survive a restart"],"dependencies":["TKT-002"]},
		{"key":"TKT-002","objective":"List widgets","requirements":["REQ-002"],
		 "acceptance_criteria":["listing returns every widget"],"dependencies":["TKT-001"]}
	]}`))
	backend.ProgramResult("ticket-plan-review", fenced(`{"verdict":"APPROVED","summary":"n/a","findings":[]}`))

	err := specengine.NewSpecEngine(backend).GenerateTicketPlan(ctx, "widget", loader)
	if err == nil {
		t.Fatal("GenerateTicketPlan accepted a cyclic ticket plan")
	}
	if code := ticketPlanError(t, err).Code; code != "CYCLIC_DEPENDENCY" {
		t.Errorf("error code = %q, want CYCLIC_DEPENDENCY (%v)", code, err)
	}
	if loader.ticketPlan != nil {
		t.Error("a cyclic ticket plan was saved")
	}
	// Rejection is deterministic and pre-review: the TicketPlanReview
	// contract is never consulted about a structurally invalid plan.
	if n := countKey(backend, "ticket-plan-review"); n != 0 {
		t.Errorf("ticket-plan-review ran %d times for a cyclic plan, want 0", n)
	}
}

// TestScenario08_MissingRequirementCoverageRejected covers the traceability
// gate: every Specification requirement must map to at least one ticket, and
// a plan that drops one is rejected with UNMAPPED_REQUIREMENT naming it.
func TestScenario08_MissingRequirementCoverageRejected(t *testing.T) {
	ctx := context.Background()
	goal := newGoal(goalBody)
	loader := newMemLoader(goal)
	loader.decisions = storageDecision(goal)

	backend := planningagent.NewFakeBackend()
	seedApprovedSpec(t, ctx, backend, loader)

	backend.ProgramResult("ticket-plan-generation", fenced(`{"tickets":[
		{"key":"TKT-001","objective":"Persist widgets","requirements":["REQ-001"],
		 "acceptance_criteria":["widgets survive a restart"],"dependencies":[]}
	]}`))
	backend.ProgramResult("ticket-plan-review", fenced(`{"verdict":"APPROVED","summary":"n/a","findings":[]}`))

	err := specengine.NewSpecEngine(backend).GenerateTicketPlan(ctx, "widget", loader)
	if err == nil {
		t.Fatal("GenerateTicketPlan accepted a plan that covers only one of two requirements")
	}
	tpErr := ticketPlanError(t, err)
	if tpErr.Code != "UNMAPPED_REQUIREMENT" {
		t.Errorf("error code = %q, want UNMAPPED_REQUIREMENT (%v)", tpErr.Code, err)
	}
	if tpErr.Message != "REQ-002" {
		t.Errorf("error names requirement %q, want REQ-002", tpErr.Message)
	}
	if loader.ticketPlan != nil {
		t.Error("an incomplete ticket plan was saved")
	}
	if n := countKey(backend, "ticket-plan-review"); n != 0 {
		t.Errorf("ticket-plan-review ran %d times for an untraceable plan, want 0", n)
	}
}

// TestScenario09_TicketPlanReviewRepair covers the bounded TicketPlanReview
// repair loop: a structurally valid but badly-scoped plan is rejected once,
// regenerated, approved on the second pass, and only the repaired plan is
// saved — with its dependency edges and estimate metadata intact.
func TestScenario09_TicketPlanReviewRepair(t *testing.T) {
	ctx := context.Background()
	goal := newGoal(goalBody)
	loader := newMemLoader(goal)
	loader.decisions = storageDecision(goal)

	backend := planningagent.NewFakeBackend()
	seedApprovedSpec(t, ctx, backend, loader)

	// One oversized ticket covering both requirements...
	backend.ProgramResult("ticket-plan-generation", fenced(`{"tickets":[
		{"key":"TKT-001","objective":"Build the whole widget service",
		 "requirements":["REQ-001","REQ-002"],
		 "acceptance_criteria":["everything works"],"dependencies":[]}
	]}`))
	backend.ProgramResult("ticket-plan-review", fenced(`{
		"verdict":"CHANGES_REQUIRED",
		"summary":"TKT-001 bundles unrelated requirements",
		"findings":[{"severity":"ERROR","ticket_key":"TKT-001","requirement":"REQ-002",
			"message":"split listing out of the persistence ticket"}]
	}`))
	// ...split into two on repair.
	backend.ProgramResult("ticket-plan-generation", fenced(`{"tickets":[
		{"key":"TKT-001","objective":"Persist widgets","requirements":["REQ-001"],
		 "acceptance_criteria":["widgets survive a restart"],"dependencies":[],
		 "estimate":{"size":"M","risk":"new_tech"}},
		{"key":"TKT-002","objective":"List widgets","requirements":["REQ-002"],
		 "acceptance_criteria":["listing returns every widget"],"dependencies":["TKT-001"],
		 "estimate":{"size":"S"}}
	]}`))
	backend.ProgramResult("ticket-plan-review", fenced(`{"verdict":"APPROVED","summary":"well scoped","findings":[]}`))

	if err := specengine.NewSpecEngine(backend).GenerateTicketPlan(ctx, "widget", loader); err != nil {
		t.Fatalf("GenerateTicketPlan: %v", err)
	}
	if n := countKey(backend, "ticket-plan-generation"); n != 2 {
		t.Errorf("ticket-plan-generation ran %d times, want 2 (initial + one repair)", n)
	}
	if n := countKey(backend, "ticket-plan-review"); n != 2 {
		t.Errorf("ticket-plan-review ran %d times, want 2 (rejection + approval)", n)
	}

	saved := loader.ticketPlan
	if saved == nil {
		t.Fatal("no ticket plan was saved")
	}
	tickets, err := ticketplan.ParseTicketPlan(saved)
	if err != nil {
		t.Fatalf("ParseTicketPlan: %v", err)
	}
	if len(tickets) != 2 {
		t.Fatalf("saved plan has %d tickets, want the repaired 2", len(tickets))
	}
	if tickets[0].Key != "TKT-001" || len(tickets[0].Dependencies) != 0 {
		t.Errorf("TKT-001 = %+v, want no dependencies", tickets[0])
	}
	if tickets[1].Key != "TKT-002" || !equalStrings(tickets[1].Dependencies, []string{"TKT-001"}) {
		t.Errorf("TKT-002 = %+v, want a dependency on TKT-001", tickets[1])
	}
	if got := saved.Estimates["TKT-001"]; got.Size != "M" || got.Risk != "new_tech" {
		t.Errorf("TKT-001 estimate = %+v, want size M / risk new_tech", got)
	}
	if got := saved.Estimates["TKT-002"]; got.Size != "S" {
		t.Errorf("TKT-002 estimate = %+v, want size S", got)
	}
	if planning.Stale(saved) {
		t.Error("the saved ticket plan is Stale: its recorded revision does not match its content")
	}
}

// TestScenario10_HumanApprovalRevisionMismatch covers the human-approval
// boundary in both directions. The Specification stage refuses to compile a
// TicketPlan from a Specification that was never approved; and once a
// TicketPlan *is* approved, hand-editing it un-binds that approval, because
// approval is stored as the revision it was granted for and is recomputed
// from content — never as a boolean. The materialization gate (see
// cmd/forge/materialize.go, which refuses unless planning.Approved holds for
// both artifacts) is exactly this predicate.
func TestScenario10_HumanApprovalRevisionMismatch(t *testing.T) {
	ctx := context.Background()
	goal := newGoal(goalBody)
	loader := newMemLoader(goal)
	loader.decisions = storageDecision(goal)

	backend := planningagent.NewFakeBackend()
	programApprovedSpec(backend)
	if err := specengine.NewSpecEngine(backend).GenerateSpec(ctx, "widget", loader); err != nil {
		t.Fatalf("GenerateSpec: %v", err)
	}
	eng := specengine.NewSpecEngine(backend)

	// A freshly generated Specification carries no approval at all, and the
	// TicketPlan stage refuses to run against it.
	if planning.Approved(loader.spec) {
		t.Fatal("a freshly generated spec must not be Approved()")
	}
	err := eng.GenerateTicketPlan(ctx, "widget", loader)
	if err == nil {
		t.Fatal("GenerateTicketPlan ran against a never-approved specification")
	}
	if !strings.Contains(err.Error(), "specification is not approved") {
		t.Errorf("error = %v, want it to name the missing specification approval", err)
	}
	if loader.ticketPlan != nil {
		t.Error("a ticket plan was generated from an unapproved specification")
	}

	// Approve the Specification at its current revision; now the TicketPlan
	// stage runs and records provenance on exactly that revision.
	approveArtifact(loader.spec)
	approvedSpecRev := loader.spec.ApprovedRevision

	backend.ProgramResult("ticket-plan-generation", fenced(`{"tickets":[
		{"key":"TKT-001","objective":"Persist widgets","requirements":["REQ-001"],
		 "acceptance_criteria":["widgets survive a restart"],"dependencies":[]},
		{"key":"TKT-002","objective":"List widgets","requirements":["REQ-002"],
		 "acceptance_criteria":["listing returns every widget"],"dependencies":["TKT-001"]}
	]}`))
	backend.ProgramResult("ticket-plan-review", fenced(`{"verdict":"APPROVED","summary":"good","findings":[]}`))
	if err := eng.GenerateTicketPlan(ctx, "widget", loader); err != nil {
		t.Fatalf("GenerateTicketPlan after approval: %v", err)
	}
	plan := loader.ticketPlan
	if rev, ok := derivedRevision(plan, "spec"); !ok || rev != approvedSpecRev {
		t.Fatalf("plan derived_from spec revision = %q (present=%v), want the approved %q", rev, ok, approvedSpecRev)
	}

	// The human approves the plan. Both artifacts now satisfy the
	// materialization gate.
	approveArtifact(plan)
	if !planning.Approved(plan) || !planning.Approved(loader.spec) {
		t.Fatal("the materialization gate is closed immediately after approving both artifacts")
	}
	approvedPlanRev := plan.ApprovedRevision

	// A human then hand-edits the approved plan (adds a ticket by hand)
	// without re-approving it.
	plan.Sections = append(plan.Sections, planning.Section{
		Heading: "Ticket: TKT-003",
		Body: "### Objective\nAdd a metrics endpoint\n\n### Requirements\nREQ-002\n\n" +
			"### Acceptance Criteria\n- metrics are exported\n\n### Dependencies\nNone",
	})

	if planning.Approved(plan) {
		t.Fatal("editing an approved ticket plan did not invalidate its approval")
	}
	if !planning.Stale(plan) {
		t.Error("a hand-edited ticket plan is not Stale()")
	}
	// The failure is a revision *mismatch*, not a missing approval: the
	// stamp is still there, it just no longer describes the content.
	if plan.ApprovedRevision != approvedPlanRev {
		t.Errorf("ApprovedRevision = %q, want it left at the approved %q", plan.ApprovedRevision, approvedPlanRev)
	}
	if plan.ApprovedRevision == planning.ComputeRevision(plan) {
		t.Error("the recomputed revision did not move on edit, so no mismatch exists")
	}

	// Re-approving at the new revision re-opens the gate — no separate
	// approval bit ever needed clearing.
	approveArtifact(plan)
	if !planning.Approved(plan) {
		t.Fatal("re-approving the edited ticket plan did not restore the approval")
	}
	if plan.ApprovedRevision == approvedPlanRev {
		t.Error("re-approval bound the old revision")
	}
}
