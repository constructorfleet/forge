package planninge2e_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/decisiongraph"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/planengine"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningagent"
	"github.com/Teagan42/forge/internal/planningsurvey"
	"github.com/Teagan42/forge/internal/specengine"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/wayfinding"
)

const goalBody = "Ship a widget service with durable storage."

// repoCtx is the Phase 1 Repository Context every planning contract in these
// scenarios is compiled against. specengine hard-codes the same BaseRevision
// internally, so using it here keeps one consistent context revision across
// the whole pipeline.
var repoCtx = agent.RepositoryContext{BaseRevision: "base"}

// TestScenario01_GoalRequiringNoDecisions walks a goal that surfaces no
// consequential unknowns all the way to a saved TicketPlan: PlanningSurvey
// proposes nothing, the Decision loop never invokes DecisionResolution at
// all, the readiness review says READY_FOR_SPEC on its first pass, and the
// Spec that comes out records provenance on the goal and the repository
// only — there is no Decision to derive from.
func TestScenario01_GoalRequiringNoDecisions(t *testing.T) {
	ctx := context.Background()
	goal := newGoal(goalBody)
	loader := newMemLoader(goal)

	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("planning-survey", bareJSON(`{"decisions":[]}`))
	backend.ProgramResult("planning-readiness-review", bareJSON(`{"status":"READY_FOR_SPEC","decisions":[]}`))
	backend.ProgramResult("specification-generation", bareJSON(`{
		"summary": "A widget service backed by SQLite",
		"requirements": [{"id":"REQ-001","description":"Widgets persist across restarts"}],
		"non_goals": ["No distributed storage"],
		"decision_refs": []
	}`))
	backend.ProgramResult("specification-review", bareJSON(`{"verdict":"APPROVED","summary":"clear","findings":[]}`))
	backend.ProgramResult("ticket-plan-generation", bareJSON(`{"tickets":[
		{"key":"TKT-001","objective":"Persist widgets","requirements":["REQ-001"],
		 "acceptance_criteria":["widgets survive a restart"],"dependencies":[]}
	]}`))
	backend.ProgramResult("ticket-plan-review", bareJSON(`{"verdict":"APPROVED","summary":"covers everything","findings":[]}`))

	// Survey -> materialize: nothing consequential to record.
	surveyPC, err := planningagent.Compile(repoCtx, []planningagent.NamedArtifact{{ID: "goal", Artifact: goal}}, nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	survey, err := planningsurvey.Propose(ctx, backend, surveyPC)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(survey.Decisions) != 0 {
		t.Fatalf("survey proposed %d decisions, want 0", len(survey.Decisions))
	}
	goalRef := decisiongraph.GoalRef{ID: "goal", Revision: goal.Revision}
	materialized, err := decisiongraph.Materialize(survey.Decisions, goalRef, nil)
	if err != nil {
		t.Fatalf("decisiongraph.Materialize: %v", err)
	}
	if len(materialized) != 0 {
		t.Fatalf("materialized %d decisions from an empty survey, want 0", len(materialized))
	}

	// Decision loop over an empty Decision set: straight to the readiness
	// review, which reports READY_FOR_SPEC and ends wayfinding.
	decisions := map[string]*planning.Artifact{}
	if err := wayfinding.Loop(ctx, backend, repoCtx, goal, goalRef, decisions, loader.persist, nil); err != nil {
		t.Fatalf("wayfinding.Loop: %v", err)
	}
	if len(decisions) != 0 {
		t.Fatalf("wayfinding recorded %d decisions, want 0: %v", len(decisions), sortedKeys(decisions))
	}

	eng := specengine.NewSpecEngine(backend)
	if err := eng.GenerateSpec(ctx, "widget", loader); err != nil {
		t.Fatalf("GenerateSpec: %v", err)
	}
	if loader.spec == nil {
		t.Fatal("no spec was saved")
	}
	// Provenance with no Decisions in it: goal + repository, nothing else.
	if len(loader.spec.DerivedFrom) != 2 {
		t.Fatalf("spec derived_from = %+v, want exactly goal + repository", loader.spec.DerivedFrom)
	}
	if rev, ok := derivedRevision(loader.spec, "goal"); !ok || rev != goal.Revision {
		t.Errorf("spec derived_from goal revision = %q (present=%v), want %q", rev, ok, goal.Revision)
	}
	if _, ok := derivedRevision(loader.spec, "repository"); !ok {
		t.Error("spec derived_from has no repository entry")
	}

	approveArtifact(loader.spec)
	if err := eng.GenerateTicketPlan(ctx, "widget", loader); err != nil {
		t.Fatalf("GenerateTicketPlan: %v", err)
	}
	if loader.ticketPlan == nil {
		t.Fatal("no ticket plan was saved")
	}
	if got := sectionBody(loader.ticketPlan, "Ticket: TKT-001"); !strings.Contains(got, "REQ-001") {
		t.Errorf("TKT-001 body does not reference REQ-001:\n%s", got)
	}

	// The full pipeline trace: one survey, one readiness review, no
	// DecisionResolution invocation at all.
	want := []string{
		"planning-survey",
		"planning-readiness-review",
		"specification-generation",
		"specification-review",
		"ticket-plan-generation",
		"ticket-plan-review",
	}
	if got := invocationKeys(backend); !equalStrings(got, want) {
		t.Errorf("invocation trace = %v, want %v", got, want)
	}
}

// TestScenario02_MultipleDependentDecisions drives a survey that proposes a
// three-node dependency chain: materialization assigns real IDs in
// dependency order and records each Decision's provenance on the one it
// depends on, the frontier exposes exactly one actionable Decision at a
// time, and the loop resolves them strictly in that order.
func TestScenario02_MultipleDependentDecisions(t *testing.T) {
	ctx := context.Background()
	goal := newGoal(goalBody)
	loader := newMemLoader(goal)

	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("planning-survey", bareJSON(`{"decisions":[
		{"temp_key":"schema","title":"Schema shape","question":"What shape is the widget row?",
		 "depends_on":["store"],"consequential":true},
		{"temp_key":"store","title":"Storage engine","question":"Which storage engine?",
		 "depends_on":[],"consequential":true},
		{"temp_key":"migrations","title":"Migration tool","question":"How do migrations run?",
		 "depends_on":["schema"],"consequential":true},
		{"temp_key":"logger","title":"Logging library","question":"Which logger?",
		 "depends_on":[],"consequential":false}
	]}`))
	backend.ProgramResult("decision-resolution", bareJSON(`{"outcome":"SQLite","rationale":"single node",
		"consequences":"no clustering","assumptions":"one writer","new_unknowns":[]}`))
	backend.ProgramResult("planning-readiness-review", bareJSON(`{"status":"READY_FOR_SPEC","decisions":[]}`))

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
	// The non-consequential proposal is dropped entirely; the remaining
	// three are numbered in dependency order (store -> schema -> migrations).
	wantIDs := []string{"001-storage-engine", "002-schema-shape", "003-migration-tool"}
	gotIDs := make([]string, 0, len(materialized))
	for _, m := range materialized {
		gotIDs = append(gotIDs, m.ID)
	}
	if !equalStrings(gotIDs, wantIDs) {
		t.Fatalf("materialized IDs = %v, want %v", gotIDs, wantIDs)
	}

	decisions := map[string]*planning.Artifact{}
	for _, m := range materialized {
		decisions[m.ID] = m.Artifact
		if err := loader.persist(m.ID, m.Artifact); err != nil {
			t.Fatalf("persist %s: %v", m.ID, err)
		}
	}

	// Each dependent Decision records provenance on the real ID and the real
	// content revision of the Decision it depends on.
	if rev, ok := derivedRevision(decisions["002-schema-shape"], "001-storage-engine"); !ok ||
		rev != decisions["001-storage-engine"].Revision {
		t.Errorf("002 derived_from 001 revision = %q (present=%v), want %q",
			rev, ok, decisions["001-storage-engine"].Revision)
	}
	if rev, ok := derivedRevision(decisions["003-migration-tool"], "002-schema-shape"); !ok ||
		rev != decisions["002-schema-shape"].Revision {
		t.Errorf("003 derived_from 002 revision = %q (present=%v), want %q",
			rev, ok, decisions["002-schema-shape"].Revision)
	}

	// Only the root Decision is actionable before anything is resolved.
	if got := decisiongraph.Frontier(decisions); !equalStrings(got, []string{"001-storage-engine"}) {
		t.Fatalf("initial frontier = %v, want [001-storage-engine]", got)
	}

	if err := wayfinding.Loop(ctx, backend, repoCtx, goal, goalRef, decisions, loader.persist, nil); err != nil {
		t.Fatalf("wayfinding.Loop: %v", err)
	}

	// Resolution happened strictly in dependency order, one fresh
	// DecisionResolution invocation per Decision.
	if got := resolvedTargets(backend); !equalStrings(got, wantIDs) {
		t.Errorf("resolution order = %v, want %v", got, wantIDs)
	}
	for _, id := range wantIDs {
		d := decisions[id]
		if !planning.Ready(d) {
			t.Errorf("decision %s is not Ready after the loop (state=%q, approved=%v)",
				id, d.State, planning.Approved(d))
		}
		if got := sectionBody(d, "Outcome"); got != "SQLite" {
			t.Errorf("decision %s Outcome = %q, want %q", id, got, "SQLite")
		}
	}
	if got := decisiongraph.Frontier(decisions); len(got) != 0 {
		t.Errorf("frontier after the loop = %v, want empty", got)
	}
	if n := countKey(backend, "planning-readiness-review"); n != 1 {
		t.Errorf("readiness review ran %d times, want 1", n)
	}
}

// TestScenario03_DecisionSpawnsBlockingDecision covers a resolution that
// surfaces a new consequential unknown: the spawned Decision is
// materialized unapproved, lands on the frontier, blocks spec generation
// while it is open, and is then resolved by the same loop.
func TestScenario03_DecisionSpawnsBlockingDecision(t *testing.T) {
	ctx := context.Background()
	goal := newGoal(goalBody)
	loader := newMemLoader(goal)

	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("planning-survey", bareJSON(`{"decisions":[
		{"temp_key":"store","title":"Storage engine","question":"Which storage engine?",
		 "depends_on":[],"consequential":true}
	]}`))
	// Resolving the storage question surfaces a brand new blocking unknown.
	backend.ProgramResult("decision-resolution", bareJSON(`{"outcome":"SQLite","rationale":"single node",
		"consequences":"needs a backup story","assumptions":"one writer",
		"new_unknowns":[{"temp_key":"backup","title":"Backup strategy",
		 "question":"How are SQLite files backed up?","depends_on":[],"consequential":true}]}`))
	backend.ProgramResult("decision-resolution", bareJSON(`{"outcome":"Litestream","rationale":"streaming replication",
		"consequences":"one more daemon","assumptions":"object storage exists","new_unknowns":[]}`))
	backend.ProgramResult("planning-readiness-review", bareJSON(`{"status":"READY_FOR_SPEC","decisions":[]}`))

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
	}

	if err := wayfinding.Loop(ctx, backend, repoCtx, goal, goalRef, decisions, loader.persist, nil); err != nil {
		t.Fatalf("wayfinding.Loop: %v", err)
	}

	if got := sortedKeys(decisions); !equalStrings(got, []string{"001-storage-engine", "002-backup-strategy"}) {
		t.Fatalf("decisions after the loop = %v, want the original plus the spawned one", got)
	}
	spawned := decisions["002-backup-strategy"]
	if got := sectionBody(spawned, "Question"); got != "How are SQLite files backed up?" {
		t.Errorf("spawned decision Question = %q", got)
	}
	if got := sectionBody(spawned, "Outcome"); got != "Litestream" {
		t.Errorf("spawned decision Outcome = %q, want Litestream", got)
	}
	// The spawned Decision was resolved after the one that spawned it, by a
	// second fresh DecisionResolution invocation.
	if got := resolvedTargets(backend); !equalStrings(got, []string{"001-storage-engine", "002-backup-strategy"}) {
		t.Errorf("resolution order = %v", got)
	}

	// The spawned Decision genuinely blocks: with it still open, the spec
	// engine refuses to compile a Specification for the Feature.
	blocked := newMemLoader(goal)
	blocked.decisions["001-storage-engine"] = decisions["001-storage-engine"]
	blocked.decisions["002-backup-strategy"] = &planning.Artifact{
		Kind:     planning.KindDecision,
		State:    "open",
		Sections: []planning.Section{{Heading: "Question", Body: "How are SQLite files backed up?"}},
	}
	err = specengine.NewSpecEngine(backend).GenerateSpec(ctx, "widget", blocked)
	if err == nil {
		t.Fatal("GenerateSpec succeeded with an open blocking decision, want an error")
	}
	if !strings.Contains(err.Error(), "002-backup-strategy is not resolved") {
		t.Errorf("GenerateSpec error = %v, want it to name the unresolved decision", err)
	}
	if blocked.spec != nil {
		t.Error("a spec was saved despite an unresolved blocking decision")
	}
}

// TestScenario04_NeedsHumanAndManualResume covers the NEEDS_HUMAN
// checkpoint and its manual resume: the Decision is paused off the frontier,
// the Planning Execution goes NEEDS_HUMAN, Forge labels and comments on the
// Feature's tracker issue, and only a genuinely new *human* comment resumes
// it — after which the operator reopens the Decision and the loop resolves
// it with the answer in hand.
func TestScenario04_NeedsHumanAndManualResume(t *testing.T) {
	ctx := context.Background()
	const featureID = "42"

	goal := newGoal(goalBody)
	loader := newMemLoader(goal)
	store := openStore(t)
	trk := newFeatureTracker()
	trk.AddIssue(domain.Issue{ID: featureID, Title: "widget feature"})

	runtime := planengine.New(store)
	exec, err := runtime.Start(ctx, featureID, "base")
	if err != nil {
		t.Fatalf("planengine.Start: %v", err)
	}

	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("decision-resolution", bareJSON(`{"needs_human":
		{"question":"Which cloud account should hold the backups?",
		 "context":"Only you can authorize the billing account."}}`))
	backend.ProgramResult("planning-readiness-review", bareJSON(`{"status":"READY_FOR_SPEC","decisions":[]}`))

	goalRef := decisiongraph.GoalRef{ID: "goal", Revision: goal.Revision}
	open := &planning.Artifact{
		Kind:        planning.KindDecision,
		State:       "proposed",
		DerivedFrom: []planning.DerivedFromEntry{{Kind: planning.KindGoal, ID: "goal", Revision: goal.Revision}},
		Sections:    []planning.Section{{Heading: "Question", Body: "Which cloud account holds backups?"}},
	}
	open.Revision = planning.ComputeRevision(open)
	decisions := map[string]*planning.Artifact{"001-backup-account": open}

	pause := &wayfinding.PauseHandler{
		ExecutionID: exec.ID,
		FeatureID:   featureID,
		Store:       store,
		Tracker:     trk,
		Label:       "forge:needs-human",
		PostComment: true,
	}
	if err := wayfinding.Loop(ctx, backend, repoCtx, goal, goalRef, decisions, loader.persist, pause.Handle); err != nil {
		t.Fatalf("wayfinding.Loop: %v", err)
	}

	paused := decisions["001-backup-account"]
	if paused.State != decisiongraph.StateNeedsHuman {
		t.Fatalf("decision state = %q, want %q", paused.State, decisiongraph.StateNeedsHuman)
	}
	if planning.Ready(paused) {
		t.Error("a paused decision must not be Ready")
	}
	if got := decisiongraph.Frontier(decisions); len(got) != 0 {
		t.Errorf("frontier = %v, want empty: a paused decision is off the frontier", got)
	}
	if got := trk.Labels(featureID); !equalStrings(got, []string{"forge:needs-human"}) {
		t.Errorf("labels on the feature issue = %v, want [forge:needs-human]", got)
	}
	comments, err := trk.GetComments(ctx, featureID)
	if err != nil {
		t.Fatalf("GetComments: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("posted %d comments, want exactly 1", len(comments))
	}
	mustContain(t, "needs-human comment", comments[0].Body, "Which cloud account should hold the backups?")
	mustContain(t, "needs-human comment", comments[0].Body, "Only you can authorize the billing account.")

	reloaded, err := store.LoadPlanningExecution(ctx, exec.ID)
	if err != nil {
		t.Fatalf("LoadPlanningExecution: %v", err)
	}
	if reloaded.Status != domain.PlanningStatusNeedsHuman {
		t.Fatalf("planning execution status = %q, want NEEDS_HUMAN", reloaded.Status)
	}
	checkpoint, err := store.GetDecisionCheckpoint(ctx, exec.ID, "001-backup-account")
	if err != nil {
		t.Fatalf("GetDecisionCheckpoint: %v", err)
	}
	if checkpoint.Question != "Which cloud account should hold the backups?" {
		t.Errorf("checkpoint.Question = %q", checkpoint.Question)
	}
	if !checkpoint.CommentPosted || checkpoint.CommentAuthor == "" {
		t.Errorf("checkpoint did not record the posted comment: %+v", checkpoint)
	}

	// Nothing has changed on the tracker yet, so a resume attempt is a no-op
	// and the execution stays parked.
	_, resumed, err := runtime.ResumePlanningExecution(ctx, exec.ID, trk)
	if err != nil {
		t.Fatalf("ResumePlanningExecution: %v", err)
	}
	if resumed {
		t.Fatal("resumed with no new human comment")
	}

	// A human answers on the Feature's issue.
	trk.PostHuman(featureID, "alice", "Use the platform-backups account.", checkpoint.CommentPostedAt.Add(time.Minute))

	after, resumed, err := runtime.ResumePlanningExecution(ctx, exec.ID, trk)
	if err != nil {
		t.Fatalf("ResumePlanningExecution: %v", err)
	}
	if !resumed {
		t.Fatal("a new human comment did not resume the planning execution")
	}
	if after.Status != domain.PlanningStatusActive {
		t.Fatalf("planning execution status = %q, want ACTIVE", after.Status)
	}
	resumedCheckpoint, err := store.GetDecisionCheckpoint(ctx, exec.ID, "001-backup-account")
	if err != nil {
		t.Fatalf("GetDecisionCheckpoint after resume: %v", err)
	}
	if resumedCheckpoint.ResumedAt == nil {
		t.Error("checkpoint.ResumedAt is nil after a successful resume")
	}
	mustContain(t, "resumed context", resumedCheckpoint.ResumedContext, "Use the platform-backups account.")

	// Manual resume's second half: the operator clears the pause (see
	// decisiongraph.StateNeedsHuman's doc comment — resume "clears it back to
	// open"), which puts the Decision back on the frontier, and a second Loop
	// call resolves it.
	paused.State = "open"
	if got := decisiongraph.Frontier(decisions); !equalStrings(got, []string{"001-backup-account"}) {
		t.Fatalf("frontier after reopening = %v, want [001-backup-account]", got)
	}
	// A fresh backend, because the resumed pass is a fresh `forge plan`
	// invocation: the loop keeps no state beyond the Decision artifacts it
	// was handed.
	resumeBackend := planningagent.NewFakeBackend()
	resumeBackend.ProgramResult("decision-resolution", bareJSON(`{"outcome":"platform-backups account",
		"rationale":"the human authorized it","consequences":"billing lands on the platform team",
		"assumptions":"","new_unknowns":[]}`))
	resumeBackend.ProgramResult("planning-readiness-review", bareJSON(`{"status":"READY_FOR_SPEC","decisions":[]}`))
	if err := wayfinding.Loop(ctx, resumeBackend, repoCtx, goal, goalRef, decisions, loader.persist, pause.Handle); err != nil {
		t.Fatalf("second wayfinding.Loop: %v", err)
	}
	final := decisions["001-backup-account"]
	if !planning.Ready(final) {
		t.Fatalf("decision is still not Ready after resume: state=%q", final.State)
	}
	if got := sectionBody(final, "Outcome"); got != "platform-backups account" {
		t.Errorf("resumed decision Outcome = %q", got)
	}
	if err := runtime.Finish(ctx, featureID, exec.ID, domain.PlanningStatusComplete); err != nil {
		t.Fatalf("planengine.Finish: %v", err)
	}
	if _, err := store.FeaturePlanningLease(ctx, featureID); err == nil {
		t.Error("planning lease was not released by Finish")
	} else if !isNotFound(err) {
		t.Errorf("FeaturePlanningLease after Finish: %v", err)
	}
}

func isNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), storage.ErrNotFound.Error())
}
