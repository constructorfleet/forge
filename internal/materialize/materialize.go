// Package materialize turns an approved TicketPlan (internal/ticketplan) —
// the intended graph — into a valid, executable tracker Issue DAG Phase 1
// (internal/engine, internal/scheduler) runs. The TicketPlan and the
// materialized Issues are distinct objects: this package is the only
// bridge between them.
//
// Materialize runs in three phases:
//
//	Phase A creates every Issue in a non-executable "materializing" state
//	(tracker.ProvenanceMaterializing), collecting the real tracker IDs
//	assigned to each temporary TKT-NNN key.
//
//	Phase B rewrites each Issue's canonical `## Dependencies` block
//	(internal/tracker's ParseDependencyBlock syntax) from temporary ticket
//	keys to the real tracker IDs collected in Phase A, and stamps each
//	Issue's `## Forge Provenance` block with project, spec revision, plan
//	revision, requirement IDs, and relevant Decision references.
//
//	Phase C re-fetches the whole materialized graph, validates it (no
//	cycles, every rewritten Dependency resolves, every provenance stamp
//	round-tripped correctly), and only then flips every Issue's status to
//	tracker.ProvenanceReady. If Phase C's validation fails, or if the
//	flip itself fails partway through, at least one Issue is left at
//	ProvenanceMaterializing — which Phase 1's execution gate
//	(tracker.ValidateExecutable) refuses to run — so a partial failure
//	never leaves an executable Issue for another Execution to grab.
package materialize

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/ticketplan"
	"github.com/Teagan42/forge/internal/tracker"
)

// Options carries the provenance stamp shared by every Issue materialized
// from one TicketPlan.
type Options struct {
	// Project is the Feature this TicketPlan belongs to.
	Project string
	// SpecRevision is the approved Specification's content revision
	// (planning.ComputeRevision) the TicketPlan was derived from.
	SpecRevision string
	// PlanRevision is the approved TicketPlan's own content revision.
	PlanRevision string
	// Decisions lists the Decision artifact IDs relevant to this
	// TicketPlan (e.g. every Decision the Specification's DerivedFrom
	// references), stamped onto every materialized Issue so Phase 1 can
	// compile Decision outcomes into its normalized context without
	// navigating the planning tree.
	Decisions []string
}

// Result is what a successful Materialize call produces.
type Result struct {
	// IssueIDs maps each ticket's temporary key (e.g. "TKT-003") to the
	// tracker-assigned Issue ID materialized for it.
	IssueIDs map[string]string
}

// Tracker is the subset of tracker.Tracker Materialize needs.
type Tracker interface {
	CreateIssue(ctx context.Context, req tracker.IssueRequest) (tracker.CreatedIssue, error)
	UpdateIssue(ctx context.Context, id string, req tracker.UpdateIssueRequest) error
	GetIssues(ctx context.Context, ids []string) ([]domain.Issue, error)
}

// PartialFailureError wraps an error encountered mid-materialization,
// preserving whichever temp-key -> tracker-ID mappings were already
// created so the caller can report the orphaned (permanently
// non-executable, since they never reach Phase C) Issues to a human for
// manual triage. GitHub Issues cannot be deleted via the API, so orphans
// from a partial Phase A/B failure are not cleaned up automatically — they
// simply never become executable.
type PartialFailureError struct {
	Phase      string
	IssueIDs   map[string]string
	Underlying error
}

func (e *PartialFailureError) Error() string {
	return fmt.Sprintf("materialize: %s: %v (created issues: %v)", e.Phase, e.Underlying, e.IssueIDs)
}

func (e *PartialFailureError) Unwrap() error { return e.Underlying }

// Materialize turns tickets (already structurally validated — see
// ticketplan.ValidateTicketPlanDeterministic: acyclic, every Dependency
// resolves to another ticket in the set, no self-Dependencies) into a
// materialized, executable tracker Issue DAG.
func Materialize(ctx context.Context, trk Tracker, tickets []ticketplan.Ticket, opts Options) (Result, error) {
	if len(tickets) == 0 {
		return Result{}, fmt.Errorf("materialize: no tickets to materialize")
	}
	if err := checkAcyclic(tickets); err != nil {
		return Result{}, err
	}

	// Phase A: create every Issue in a non-executable materializing state,
	// collecting real tracker IDs for each temporary key.
	issueIDs := make(map[string]string, len(tickets))
	for _, t := range tickets {
		body := renderInitialBody(t, opts)
		created, err := trk.CreateIssue(ctx, tracker.IssueRequest{
			Title: t.Key + ": " + t.Objective,
			Body:  body,
		})
		if err != nil {
			return Result{}, &PartialFailureError{Phase: "A (create)", IssueIDs: issueIDs, Underlying: err}
		}
		issueIDs[t.Key] = created.ID
	}

	// Phase B: rewrite temporary ticket keys to tracker IDs in the
	// canonical `## Dependencies` block, and stamp full provenance.
	for _, t := range tickets {
		deps := make([]string, len(t.Dependencies))
		for i, d := range t.Dependencies {
			id, ok := issueIDs[d]
			if !ok {
				return Result{}, &PartialFailureError{
					Phase: "B (rewrite dependencies)", IssueIDs: issueIDs,
					Underlying: fmt.Errorf("ticket %s depends on unresolved key %s", t.Key, d),
				}
			}
			deps[i] = id
		}
		body := renderMaterializingBody(t, deps, opts)
		if err := trk.UpdateIssue(ctx, issueIDs[t.Key], tracker.UpdateIssueRequest{Body: body}); err != nil {
			return Result{}, &PartialFailureError{Phase: "B (rewrite dependencies)", IssueIDs: issueIDs, Underlying: err}
		}
	}

	// Phase C: re-fetch and validate the materialized graph. Issues become
	// executable only once the whole graph validates — and only once every
	// Issue has been individually flipped to ready.
	if err := validateMaterializedGraph(ctx, trk, tickets, issueIDs); err != nil {
		return Result{}, &PartialFailureError{Phase: "C (validate)", IssueIDs: issueIDs, Underlying: err}
	}

	for _, t := range tickets {
		deps := make([]string, len(t.Dependencies))
		for i, d := range t.Dependencies {
			deps[i] = issueIDs[d]
		}
		body := renderReadyBody(t, deps, opts)
		if err := trk.UpdateIssue(ctx, issueIDs[t.Key], tracker.UpdateIssueRequest{Body: body}); err != nil {
			return Result{}, &PartialFailureError{Phase: "C (commit ready)", IssueIDs: issueIDs, Underlying: err}
		}
	}

	return Result{IssueIDs: issueIDs}, nil
}

// checkAcyclic defensively re-verifies the temp-key dependency graph is
// acyclic before any tracker I/O begins. Callers are expected to have
// already run ticketplan.ValidateTicketPlanDeterministic; this is a cheap
// second gate against Materialize being invoked on unvalidated input.
func checkAcyclic(tickets []ticketplan.Ticket) error {
	byKey := make(map[string]ticketplan.Ticket, len(tickets))
	for _, t := range tickets {
		byKey[t.Key] = t
	}

	const (
		unvisited = 0
		visiting  = 1
		done      = 2
	)
	state := make(map[string]int, len(tickets))
	var visit func(key string) error
	visit = func(key string) error {
		state[key] = visiting
		for _, dep := range byKey[key].Dependencies {
			if _, ok := byKey[dep]; !ok {
				return fmt.Errorf("materialize: ticket %s depends on unknown ticket %s", key, dep)
			}
			switch state[dep] {
			case visiting:
				return fmt.Errorf("materialize: cyclic dependency involving ticket %s", dep)
			case unvisited:
				if err := visit(dep); err != nil {
					return err
				}
			}
		}
		state[key] = done
		return nil
	}

	keys := make([]string, 0, len(tickets))
	for _, t := range tickets {
		keys = append(keys, t.Key)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if state[k] == unvisited {
			if err := visit(k); err != nil {
				return err
			}
		}
	}
	return nil
}

func renderInitialBody(t ticketplan.Ticket, opts Options) string {
	return renderTicketSections(t) + tracker.RenderForgeProvenance(tracker.ForgeProvenance{
		Status:       tracker.ProvenanceMaterializing,
		TempKey:      t.Key,
		Project:      opts.Project,
		SpecRevision: opts.SpecRevision,
		PlanRevision: opts.PlanRevision,
		Requirements: t.Requirements,
		Decisions:    opts.Decisions,
	})
}

func renderMaterializingBody(t ticketplan.Ticket, deps []string, opts Options) string {
	return renderTicketSections(t) + renderDependencies(deps) + tracker.RenderForgeProvenance(tracker.ForgeProvenance{
		Status:       tracker.ProvenanceMaterializing,
		TempKey:      t.Key,
		Project:      opts.Project,
		SpecRevision: opts.SpecRevision,
		PlanRevision: opts.PlanRevision,
		Requirements: t.Requirements,
		Decisions:    opts.Decisions,
	})
}

func renderReadyBody(t ticketplan.Ticket, deps []string, opts Options) string {
	return renderTicketSections(t) + renderDependencies(deps) + tracker.RenderForgeProvenance(tracker.ForgeProvenance{
		Status:       tracker.ProvenanceReady,
		TempKey:      t.Key,
		Project:      opts.Project,
		SpecRevision: opts.SpecRevision,
		PlanRevision: opts.PlanRevision,
		Requirements: t.Requirements,
		Decisions:    opts.Decisions,
	})
}

func renderTicketSections(t ticketplan.Ticket) string {
	var b strings.Builder
	b.WriteString("### Objective\n")
	b.WriteString(t.Objective)
	b.WriteString("\n\n### Requirements\n")
	for _, r := range t.Requirements {
		b.WriteString("- ")
		b.WriteString(r)
		b.WriteString("\n")
	}
	b.WriteString("\n### Acceptance Criteria\n")
	for _, ac := range t.AcceptanceCriteria {
		b.WriteString("- ")
		b.WriteString(ac)
		b.WriteString("\n")
	}
	if len(t.ImplementationContext) > 0 {
		b.WriteString("\n### Implementation Context\n")
		for _, note := range t.ImplementationContext {
			b.WriteString("- ")
			b.WriteString(note)
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	return b.String()
}

func renderDependencies(deps []string) string {
	if len(deps) == 0 {
		return "## Dependencies: None\n\n"
	}
	var b strings.Builder
	b.WriteString("## Dependencies\n")
	for _, id := range deps {
		b.WriteString("- #")
		b.WriteString(id)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	return b.String()
}

// validateMaterializedGraph re-fetches every materialized Issue and
// validates: the rewritten `## Dependencies` blocks parse and form an
// acyclic graph, every ticket's Dependencies resolved to exactly the
// tracker IDs Phase A/B intended, and every Issue's `## Forge Provenance`
// block round-tripped as expected (still materializing, not yet ready —
// Phase C has not committed the flip yet when this runs).
func validateMaterializedGraph(ctx context.Context, trk Tracker, tickets []ticketplan.Ticket, issueIDs map[string]string) error {
	ids := make([]string, 0, len(tickets))
	for _, t := range tickets {
		ids = append(ids, issueIDs[t.Key])
	}

	issues, err := trk.GetIssues(ctx, ids)
	if err != nil {
		return fmt.Errorf("re-fetch materialized issues: %w", err)
	}
	byID := make(map[string]domain.Issue, len(issues))
	for _, issue := range issues {
		byID[issue.ID] = issue
	}

	domainIssues := make([]domain.Issue, 0, len(issues))
	for _, t := range tickets {
		id := issueIDs[t.Key]
		issue, ok := byID[id]
		if !ok {
			return fmt.Errorf("issue %s (ticket %s) missing from re-fetch", id, t.Key)
		}

		parsedDeps, err := tracker.ParseDependencyBlock(issue.Body)
		if err != nil {
			return fmt.Errorf("issue %s (ticket %s): %w", id, t.Key, err)
		}
		wantDeps := make([]string, len(t.Dependencies))
		for i, d := range t.Dependencies {
			wantDeps[i] = issueIDs[d]
		}
		if !sameSet(parsedDeps, wantDeps) {
			return fmt.Errorf("issue %s (ticket %s): dependencies %v did not round-trip (want %v)",
				id, t.Key, parsedDeps, wantDeps)
		}

		prov, err := tracker.ParseForgeProvenance(issue.Body)
		if err != nil {
			return fmt.Errorf("issue %s (ticket %s): %w", id, t.Key, err)
		}
		if prov == nil {
			return fmt.Errorf("issue %s (ticket %s): missing Forge Provenance block", id, t.Key)
		}
		if prov.TempKey != t.Key {
			return fmt.Errorf("issue %s: provenance temp_key %q does not match ticket %s", id, prov.TempKey, t.Key)
		}

		deps := make([]domain.Dependency, len(parsedDeps))
		for i, dep := range parsedDeps {
			deps[i] = domain.Dependency{IssueID: id, DependsOnID: dep}
		}
		domainIssues = append(domainIssues, domain.Issue{ID: id, Dependencies: deps})
	}

	if _, err := tracker.BuildDAG(domainIssues); err != nil {
		return fmt.Errorf("materialized dependency graph: %w", err)
	}

	return nil
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	countA := map[string]int{}
	for _, v := range a {
		countA[v]++
	}
	for _, v := range b {
		countA[v]--
	}
	for _, c := range countA {
		if c != 0 {
			return false
		}
	}
	return true
}
