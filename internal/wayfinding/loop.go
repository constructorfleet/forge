// Package wayfinding implements the sequential decision loop (ticket 14):
// the recursive heart of wayfinding, walking a Feature's Decision dependency
// DAG one ready node at a time -- resolve, record, discover, recompute the
// frontier, repeat -- until nothing more is currently resolvable. Per D6 it
// is a deliberately sequential engine, not Scheduler fan-out: one fresh
// DecisionResolution invocation per Decision, decisions sequential within a
// Feature.
package wayfinding

import (
	"context"
	"fmt"
	"sort"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/decisiongraph"
	"github.com/Teagan42/forge/internal/decisionresolution"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningagent"
	"github.com/Teagan42/forge/internal/planningreadiness"
)

// Persist is called by Loop every time it changes a Decision artifact's
// durable content -- either applying a resolution to an existing Decision
// or materializing a newly spawned one -- so a real caller can write it to
// disk (or wherever Planning Artifacts live) immediately. This is what
// makes the loop resumable across process restarts: Loop itself holds no
// state beyond its decisions argument, so if the process dies mid-loop,
// everything Persist has already been called for is durable, and calling
// Loop again against the reloaded decisions picks up exactly where it left
// off -- Frontier recomputes from current content, and nothing already
// resolved is re-resolved.
type Persist func(id string, artifact *planning.Artifact) error

// Loop resolves every currently-ready Decision in decisions, one at a time
// in dependency order, each a fresh decisionresolution.Resolve invocation
// built from a freshly compiled PlanningContext -- no reliance on prior
// conversation. Each resolution is applied onto its Decision artifact
// (decisiongraph.ApplyResolution) and persisted; consequential
// new_unknowns are materialized into new Decision artifacts
// (decisiongraph.Materialize), added to decisions, and persisted too, which
// may put more Decisions on the frontier. Loop repeats until the frontier
// is empty and returns.
//
// decisions is mutated in place -- both the map (new Decisions are added)
// and the map's Artifact values (resolved Decisions are replaced) -- so
// callers should treat it as Loop's single source of truth for the
// in-progress Decision set. goalID and goalRevision identify the Feature's
// goal Artifact for materialized Decisions' provenance (decisiongraph.GoalRef);
// goalArtifact may be nil if no goal Artifact has been compiled (Loop still
// runs, just without goal content in the prompt).
//
// A Decision that resolves to NEEDS_HUMAN is never applied
// (decisiongraph.ApplyResolution) or materialized: onNeedsHuman is invoked
// instead (see PauseHandler for Forge's own checkpoint/tracker/status
// implementation), the Decision it returns is persisted paused, and Loop
// continues on to the rest of the frontier -- only that Decision's path is
// paused; independent paths keep resolving in the same Loop call (ticket
// 15a). onNeedsHuman must be non-nil if any Decision might report
// NEEDS_HUMAN.
func Loop(
	ctx context.Context,
	backend planningagent.Backend,
	repo agent.RepositoryContext,
	goalArtifact *planning.Artifact,
	goalRef decisiongraph.GoalRef,
	decisions map[string]*planning.Artifact,
	persist Persist,
	onNeedsHuman NeedsHumanHandler,
) error {
	for {
		frontier := decisiongraph.Frontier(decisions)
		if len(frontier) == 0 {
			ready, err := runReadinessReview(ctx, backend, repo, goalArtifact, goalRef, decisions, persist)
			if err != nil {
				return err
			}
			if ready {
				return nil
			}
			continue
		}
		targetID := frontier[0]

		pc, err := compilePlanningContext(repo, goalArtifact, decisions)
		if err != nil {
			return fmt.Errorf("wayfinding: compile planning context for %s: %w", targetID, err)
		}

		res, err := decisionresolution.Resolve(ctx, backend, decisionresolution.Request{
			Context:  pc,
			TargetID: targetID,
		})
		if err != nil {
			return fmt.Errorf("wayfinding: resolve %s: %w", targetID, err)
		}

		if res.NeedsHuman != nil {
			if onNeedsHuman == nil {
				return fmt.Errorf("wayfinding: decision %s needs human input but no NeedsHumanHandler is configured", targetID)
			}
			paused, err := onNeedsHuman(ctx, targetID, decisions[targetID], *res.NeedsHuman)
			if err != nil {
				return fmt.Errorf("wayfinding: pause %s for human input: %w", targetID, err)
			}
			decisions[targetID] = paused
			if err := persist(targetID, paused); err != nil {
				return fmt.Errorf("wayfinding: persist %s: %w", targetID, err)
			}
			continue
		}

		resolved := decisiongraph.ApplyResolution(decisions[targetID], res)
		decisions[targetID] = resolved
		if err := persist(targetID, resolved); err != nil {
			return fmt.Errorf("wayfinding: persist %s: %w", targetID, err)
		}

		if len(res.NewUnknowns) == 0 {
			continue
		}

		materialized, err := decisiongraph.Materialize(res.NewUnknowns, goalRef, existingIDs(decisions))
		if err != nil {
			return fmt.Errorf("wayfinding: materialize new unknowns from %s: %w", targetID, err)
		}
		for _, m := range materialized {
			decisions[m.ID] = m.Artifact
			if err := persist(m.ID, m.Artifact); err != nil {
				return fmt.Errorf("wayfinding: persist %s: %w", m.ID, err)
			}
		}
	}
}

// compilePlanningContext compiles a PlanningContext from repo, goalArtifact
// (if any), and every currently known Decision, so each Resolve invocation
// sees the full, current Decision set -- including ones not yet resolved,
// mirroring how PlanningSurvey's own prompt lists existing Decisions.
func compilePlanningContext(repo agent.RepositoryContext, goalArtifact *planning.Artifact, decisions map[string]*planning.Artifact) (planningagent.PlanningContext, error) {
	artifacts := make([]planningagent.NamedArtifact, 0, len(decisions)+1)
	if goalArtifact != nil {
		artifacts = append(artifacts, planningagent.NamedArtifact{ID: "goal", Artifact: goalArtifact})
	}
	for id, d := range decisions {
		artifacts = append(artifacts, planningagent.NamedArtifact{ID: id, Artifact: d})
	}
	return planningagent.Compile(repo, artifacts, nil)
}

// existingIDs returns decisions' keys, sorted, for decisiongraph.Materialize's
// numbering/slug-collision avoidance.
func existingIDs(decisions map[string]*planning.Artifact) []string {
	ids := make([]string, 0, len(decisions))
	for id := range decisions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// runReadinessReview runs PlanningReadinessReview once decisiongraph.Frontier
// empties (ticket 15): a fresh, independent check of whether the Feature's
// Decisions are actually sufficient to write a spec from, rather than
// assuming an empty frontier means wayfinding is done. On
// StatusReadyForSpec it reports ready = true and Loop returns. Otherwise
// the review's proposed Decisions are materialized and persisted exactly
// like a DecisionResolution's new_unknowns, putting them back on the
// frontier, and ready = false tells Loop's caller to keep going.
//
// A NOT_READY verdict that proposes no Decisions at all would otherwise
// leave the frontier empty forever, causing every subsequent iteration to
// re-run the same review against the same unchanged Decision set -- an
// infinite loop. runReadinessReview treats that as an error instead: the
// reviewer must either say the plan is ready or say what's missing.
func runReadinessReview(
	ctx context.Context,
	backend planningagent.Backend,
	repo agent.RepositoryContext,
	goalArtifact *planning.Artifact,
	goalRef decisiongraph.GoalRef,
	decisions map[string]*planning.Artifact,
	persist Persist,
) (ready bool, err error) {
	pc, err := compilePlanningContext(repo, goalArtifact, decisions)
	if err != nil {
		return false, fmt.Errorf("wayfinding: compile planning context for readiness review: %w", err)
	}

	res, err := planningreadiness.Review(ctx, backend, pc)
	if err != nil {
		return false, fmt.Errorf("wayfinding: readiness review: %w", err)
	}

	if res.Status == planningreadiness.StatusReadyForSpec {
		return true, nil
	}

	materialized, err := decisiongraph.Materialize(res.Decisions, goalRef, existingIDs(decisions))
	if err != nil {
		return false, fmt.Errorf("wayfinding: materialize readiness review decisions: %w", err)
	}
	if len(materialized) == 0 {
		return false, fmt.Errorf("wayfinding: readiness review reported %s but proposed no decisions to resolve", planningreadiness.StatusNotReady)
	}
	for _, m := range materialized {
		decisions[m.ID] = m.Artifact
		if err := persist(m.ID, m.Artifact); err != nil {
			return false, fmt.Errorf("wayfinding: persist %s: %w", m.ID, err)
		}
	}
	return false, nil
}
