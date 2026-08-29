package planninge2e_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/specengine"
	"github.com/Teagan42/forge/internal/tracker"
)

// TestScenario14_RecoveryAfterLocalStateLoss covers losing everything Forge
// keeps locally — a wiped `.forge/` directory: the Planning Artifact files
// and the SQLite runtime database — while the canonical tracker (and the git
// repository) are untouched.
//
// The guarantee this pins is deliberately scoped to *post-materialization*
// recovery. Once a TicketPlan has been materialized, every fact Phase 1
// needs lives in the Issue bodies themselves (the `## Dependencies` block and
// the `## Forge Provenance` stamp — see internal/tracker's provenance doc
// comment: Phase 1 compiles its context "without ever navigating the planning
// tree"), so a fresh Forge with an empty local state directory can pick the
// graph straight back up. Planning content that had not been materialized yet
// only ever lived locally and is genuinely gone; the second half of this test
// pins that boundary rather than overclaiming it.
func TestScenario14_RecoveryAfterLocalStateLoss(t *testing.T) {
	ctx := context.Background()
	p := runFullPipeline(t, ctx)
	first, second := p.issueIDs["TKT-001"], p.issueIDs["TKT-002"]

	// Snapshot what the canonical tracker holds before the loss, so the
	// post-loss assertions compare against real pre-loss values.
	specRev, planRev := p.opts.SpecRevision, p.opts.PlanRevision
	beforeBodies := map[string]string{
		first:  issueBody(t, p.trk, first),
		second: issueBody(t, p.trk, second),
	}

	// --- the local state directory is wiped ---
	//
	// The in-memory ArtifactLoader is Forge's `.forge/features/<id>/*.md`;
	// dropping it is the file half. The runtime database is the SQLite half:
	// every later assertion runs against a brand new store (newPhase1 opens
	// one in a fresh temp directory). The FakeTracker instance is deliberately
	// the same object throughout — nothing remote was lost.
	p.loader.wipe()

	// The Issues survive verbatim: same bodies, same dependency edges, same
	// provenance stamp, all still parseable from tracker state alone.
	for id, before := range beforeBodies {
		after := issueBody(t, p.trk, id)
		if after != before {
			t.Errorf("issue %s body changed across local state loss:\nbefore:\n%s\nafter:\n%s", id, before, after)
		}
	}
	deps, err := tracker.ParseDependencyBlock(issueBody(t, p.trk, second))
	if err != nil {
		t.Fatalf("ParseDependencyBlock after state loss: %v", err)
	}
	if !equalStrings(deps, []string{first}) {
		t.Errorf("recovered dependencies for %s = %v, want [%s]", second, deps, first)
	}

	for _, id := range []string{first, second} {
		body := issueBody(t, p.trk, id)
		prov, err := tracker.ParseForgeProvenance(body)
		if err != nil {
			t.Fatalf("ParseForgeProvenance(%s) after state loss: %v", id, err)
		}
		if prov == nil {
			t.Fatalf("issue %s lost its Forge Provenance block", id)
		}
		if prov.Status != tracker.ProvenanceReady {
			t.Errorf("issue %s status = %q, want %q", id, prov.Status, tracker.ProvenanceReady)
		}
		// The provenance stamp still names the Feature, the approved spec and
		// plan revisions, and the Decisions — the whole reason Phase 1 never
		// needs the planning tree.
		if prov.Project != pipelineFeatureID {
			t.Errorf("issue %s project = %q, want %q", id, prov.Project, pipelineFeatureID)
		}
		if prov.SpecRevision != specRev || prov.PlanRevision != planRev {
			t.Errorf("issue %s revisions = (%q, %q), want (%q, %q)",
				id, prov.SpecRevision, prov.PlanRevision, specRev, planRev)
		}
		if !equalStrings(prov.Decisions, []string{"001-storage-engine"}) {
			t.Errorf("issue %s decisions = %v, want [001-storage-engine]", id, prov.Decisions)
		}
		// Phase 1's handoff gate still admits them.
		if err := tracker.ValidateExecutable(id, body); err != nil {
			t.Errorf("issue %s is no longer executable after local state loss: %v", id, err)
		}
	}

	// A brand new Forge — fresh, empty SQLite store, fresh workspace manager,
	// same tracker — executes the recovered graph.
	recovered := newPhase1(t, p.trk)
	if executions, err := recovered.store.ListExecutions(ctx); err != nil {
		t.Fatalf("ListExecutions: %v", err)
	} else if len(executions) != 0 {
		t.Fatalf("the recovery store is not empty: %d executions", len(executions))
	}
	recovered.agent.ProgramResult(first, agent.AgentResult{Status: agent.StatusImplemented, Summary: "persisted widgets"})
	result, err := recovered.eng.Execute(ctx, first, recovered.base)
	if err != nil {
		t.Fatalf("Execute after local state loss: %v", err)
	}
	if result.Issue.State != domain.StateCommitting {
		t.Fatalf("final state = %s, want COMMITTING", result.Issue.State)
	}
	// The recovered Issue was reconstructed from the tracker, provenance and
	// all: the new store now holds it.
	persisted, err := recovered.store.GetIssue(ctx, result.ExecutionID, first)
	if err != nil {
		t.Fatalf("GetIssue from the recovery store: %v", err)
	}
	if prov, err := tracker.ParseForgeProvenance(persisted.Body); err != nil || prov == nil {
		t.Fatalf("persisted recovered issue lost its provenance (%v)", err)
	} else if prov.PlanRevision != planRev {
		t.Errorf("persisted plan revision = %q, want %q", prov.PlanRevision, planRev)
	}

	// --- the boundary of the guarantee ---
	//
	// Un-materialized planning content is not recoverable: the goal,
	// Decisions, Spec, and TicketPlan only ever lived in the wiped local
	// files. Nothing on the tracker can reconstitute them, and the compiler
	// says so rather than proceeding on a guess.
	if _, err := p.loader.LoadGoal(ctx, pipelineFeatureID); err == nil {
		t.Error("the goal artifact survived a local state wipe, which it cannot")
	}
	decisions, err := p.loader.LoadDecisions(ctx, pipelineFeatureID)
	if err != nil {
		t.Fatalf("LoadDecisions: %v", err)
	}
	if len(decisions) != 0 {
		t.Errorf("decisions survived a local state wipe: %v", sortedKeys(decisions))
	}
	lostSpec, err := p.loader.LoadSpec(ctx, pipelineFeatureID)
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	if lostSpec != nil {
		t.Error("the spec artifact survived a local state wipe, which it cannot")
	}
	err = specengine.NewSpecEngine(nil).GenerateTicketPlan(ctx, pipelineFeatureID, p.loader)
	if err == nil {
		t.Fatal("the ticket plan stage ran against wiped planning state")
	}
	if !strings.Contains(err.Error(), "no spec found for feature "+pipelineFeatureID) {
		t.Errorf("error = %v, want it to report the missing spec rather than guess", err)
	}
}
