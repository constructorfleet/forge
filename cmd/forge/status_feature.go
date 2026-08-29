package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Teagan42/forge/internal/decisiongraph"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/storage"
)

// isFeatureID reports whether id names a Feature with a Planning Artifact
// directory on disk (.forge/features/<id>), the signal `forge status` uses
// to route to the feature-scoped report instead of treating id as an
// Execution ID.
func isFeatureID(id string) bool {
	info, err := os.Stat(filepath.Join(".forge", "features", id))
	return err == nil && info.IsDir()
}

// FeatureStatusReport is `forge status <feature-id>`'s answer (ticket 21,
// acceptance item 3): the Feature's current planning Stage, its active and
// paused Decisions, artifact approval and staleness, the most recent
// Planning Execution (if any), and the single deterministic next action a
// human or `forge plan` would take from here. Every field is derived fresh
// from durable state (Planning Artifacts on disk, the Planning Execution
// table) each time it's computed -- nothing here is itself stored.
type FeatureStatusReport struct {
	FeatureID string
	Stage     planning.Stage

	// Frontier lists Decisions actionable now (decisiongraph.Frontier):
	// not yet resolved, not paused, with every dependency already Ready.
	Frontier []string
	// PausedDecisions lists Decisions paused on NEEDS_HUMAN.
	PausedDecisions []string

	SpecExists         bool
	SpecApproved       bool
	TicketPlanExists   bool
	TicketPlanApproved bool

	// StaleArtifacts names every Planning Artifact (by its file path
	// relative to the Feature's directory) that is either hand-edited out
	// of sync with its own recorded revision (planning.Stale) or derived
	// from upstream content that has since changed (its DerivedFrom
	// entries no longer match the referenced Artifact's current revision).
	StaleArtifacts []string

	// PlanningExecutionID/PlanningStatus describe the Feature's most
	// recently started Planning Execution, if any has ever run. Empty when
	// `forge plan` has never been invoked for this Feature.
	PlanningExecutionID string
	PlanningStatus      string

	// NextAction is the single deterministic next step -- exactly what a
	// subsequent `forge plan`/`forge approve`/`forge resume`/`forge
	// materialize` invocation would do -- derived purely from the fields
	// above.
	NextAction string
}

// loadFeatureStatus computes a FeatureStatusReport for featureID from its
// Planning Artifacts on disk and its Planning Executions in store.
func loadFeatureStatus(ctx context.Context, store storage.Store, featureID string) (FeatureStatusReport, error) {
	loader := &fileArtifactLoader{featureID: featureID}

	goal, err := loader.LoadGoal(ctx, featureID)
	if err != nil && !os.IsNotExist(err) {
		return FeatureStatusReport{}, fmt.Errorf("load goal: %w", err)
	}

	decisions, err := loader.LoadDecisions(ctx, featureID)
	if err != nil {
		return FeatureStatusReport{}, fmt.Errorf("load decisions: %w", err)
	}

	specArtifact, err := loader.LoadSpec(ctx, featureID)
	if err != nil {
		return FeatureStatusReport{}, fmt.Errorf("load spec: %w", err)
	}

	ticketPlan, err := loader.LoadTicketPlan(ctx, featureID)
	if err != nil {
		return FeatureStatusReport{}, fmt.Errorf("load ticket plan: %w", err)
	}

	decisionList := make([]*planning.Artifact, 0, len(decisions))
	for _, d := range decisions {
		decisionList = append(decisionList, d)
	}

	report := FeatureStatusReport{
		FeatureID:          featureID,
		Stage:              planning.DeriveStage(goal, decisionList, specArtifact, ticketPlan),
		Frontier:           decisiongraph.Frontier(decisions),
		PausedDecisions:    pausedDecisions(decisions),
		SpecExists:         specArtifact != nil,
		SpecApproved:       specArtifact != nil && planning.Approved(specArtifact),
		TicketPlanExists:   ticketPlan != nil,
		TicketPlanApproved: ticketPlan != nil && planning.Approved(ticketPlan),
		StaleArtifacts:     staleArtifacts(goal, decisions, specArtifact, ticketPlan),
	}

	execs, err := store.ListPlanningExecutionsByFeature(ctx, featureID)
	if err != nil {
		return FeatureStatusReport{}, fmt.Errorf("list planning executions: %w", err)
	}
	if latest, ok := latestPlanningExecution(execs); ok {
		report.PlanningExecutionID = latest.ID
		report.PlanningStatus = string(latest.Status)
	}

	report.NextAction = nextAction(report, goal)
	return report, nil
}

// pausedDecisions returns the sorted IDs of every Decision paused on
// NEEDS_HUMAN.
func pausedDecisions(decisions map[string]*planning.Artifact) []string {
	var ids []string
	for id, d := range decisions {
		if d.State == decisiongraph.StateNeedsHuman {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// latestPlanningExecution returns the most recently started Planning
// Execution in execs, if any.
func latestPlanningExecution(execs []domain.PlanningExecution) (domain.PlanningExecution, bool) {
	if len(execs) == 0 {
		return domain.PlanningExecution{}, false
	}
	latest := execs[0]
	for _, e := range execs[1:] {
		if e.StartedAt.After(latest.StartedAt) {
			latest = e
		}
	}
	return latest, true
}

// staleArtifacts names every Planning Artifact that is out of sync with
// its own recorded content (planning.Stale -- a hand-edit that bypassed
// recomputing Revision) or with the upstream Artifacts it was derived
// from (a DerivedFrom entry's recorded Revision no longer matches the
// referenced Artifact's current Revision).
func staleArtifacts(goal *planning.Artifact, decisions map[string]*planning.Artifact, spec, ticketPlan *planning.Artifact) []string {
	var stale []string

	if goal != nil && planning.Stale(goal) {
		stale = append(stale, "goal.md")
	}

	ids := make([]string, 0, len(decisions))
	for id := range decisions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if planning.Stale(decisions[id]) {
			stale = append(stale, fmt.Sprintf("decisions/%s.md", id))
		}
	}

	if spec != nil {
		current := map[string]string{}
		if goal != nil {
			current["goal"] = goal.Revision
		}
		for id, d := range decisions {
			current[id] = d.Revision
		}
		if planning.Stale(spec) || !derivedFromCurrent(spec.DerivedFrom, current) {
			stale = append(stale, "spec.md")
		}
	}

	if ticketPlan != nil {
		current := map[string]string{}
		if spec != nil {
			current["spec"] = spec.Revision
		}
		if planning.Stale(ticketPlan) || !derivedFromCurrent(ticketPlan.DerivedFrom, current) {
			stale = append(stale, "ticket-plan.md")
		}
	}

	return stale
}

// derivedFromCurrent reports whether every entry in entries that names a
// currently-known Artifact (by ID, via current) still records that
// Artifact's current Revision. Entries naming an Artifact not present in
// current (e.g. "repository", whose ContextRevision has no stable
// standalone value to compare against) are ignored.
func derivedFromCurrent(entries []planning.DerivedFromEntry, current map[string]string) bool {
	for _, e := range entries {
		want, ok := current[e.ID]
		if ok && e.Revision != want {
			return false
		}
	}
	return true
}

// nextAction derives the single deterministic next step a human or `forge
// plan` should take, in the same priority order runPlan itself walks the
// pipeline in.
func nextAction(r FeatureStatusReport, goal *planning.Artifact) string {
	switch {
	case goal == nil:
		return fmt.Sprintf("create .forge/features/%s/goal.md", r.FeatureID)
	case len(r.PausedDecisions) > 0:
		resume := r.PlanningExecutionID
		if resume == "" {
			resume = "<execution-id>"
		}
		return fmt.Sprintf("answer needs-human decision(s) %s, then run `forge resume %s`", strings.Join(r.PausedDecisions, ", "), resume)
	case len(r.Frontier) > 0:
		return fmt.Sprintf("run `forge plan %s` to continue resolving decisions", r.FeatureID)
	case !r.SpecExists:
		return fmt.Sprintf("run `forge plan %s --until spec` to generate the specification", r.FeatureID)
	case !r.SpecApproved:
		return fmt.Sprintf("run `forge approve %s spec`", r.FeatureID)
	case !r.TicketPlanExists:
		return fmt.Sprintf("run `forge plan %s` to generate the ticket plan", r.FeatureID)
	case !r.TicketPlanApproved:
		return fmt.Sprintf("run `forge approve %s tickets`", r.FeatureID)
	default:
		return fmt.Sprintf("run `forge materialize %s`", r.FeatureID)
	}
}

// runFeatureStatus implements `forge status <feature-id>`.
func runFeatureStatus(featureID, dbPath string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	store, err := openStore(ctx, dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge status: %v\n", err)
		return 1
	}
	defer func() { _ = store.Close() }()

	report, err := loadFeatureStatus(ctx, store, featureID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge status: %v\n", err)
		return 1
	}

	printFeatureStatus(os.Stdout, report)
	return 0
}

func printFeatureStatus(w io.Writer, r FeatureStatusReport) {
	fmt.Fprintf(w, "feature %s\n", r.FeatureID)
	fmt.Fprintf(w, "  stage: %s\n", r.Stage)
	if len(r.Frontier) > 0 {
		fmt.Fprintf(w, "  active decisions: %s\n", strings.Join(r.Frontier, ", "))
	}
	if len(r.PausedDecisions) > 0 {
		fmt.Fprintf(w, "  needs-human decisions: %s\n", strings.Join(r.PausedDecisions, ", "))
	}
	fmt.Fprintf(w, "  spec: exists=%t approved=%t\n", r.SpecExists, r.SpecApproved)
	fmt.Fprintf(w, "  ticket-plan: exists=%t approved=%t\n", r.TicketPlanExists, r.TicketPlanApproved)
	if len(r.StaleArtifacts) > 0 {
		fmt.Fprintf(w, "  stale artifacts: %s\n", strings.Join(r.StaleArtifacts, ", "))
	}
	if r.PlanningExecutionID != "" {
		fmt.Fprintf(w, "  planning execution: %s (%s)\n", r.PlanningExecutionID, r.PlanningStatus)
	}
	fmt.Fprintf(w, "  next action: %s\n", r.NextAction)
}
