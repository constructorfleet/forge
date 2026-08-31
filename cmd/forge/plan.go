package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"

	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/decisiongraph"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/planengine"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningagent"
	"github.com/Teagan42/forge/internal/replan"
	"github.com/Teagan42/forge/internal/repocontext"
	"github.com/Teagan42/forge/internal/specengine"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
	"github.com/Teagan42/forge/internal/wayfinding"
)

const planUsage = `Usage: forge plan <feature-id> [--until wayfinding|spec|tickets]

Run the planning compiler pipeline for a Feature: resolve Decisions
(wayfinding), generate a Specification, then generate a Ticket Plan.

forge plan is idempotent -- re-running it resumes from whatever valid
Planning Artifact state already exists on disk and never regenerates an
artifact that already exists. It stops cleanly at each human gate:

  - a Decision that needs human input (see 'forge resume <execution-id>')
  - an unapproved spec.md (see 'forge approve <feature-id> spec')
  - an unapproved ticket-plan.md (see 'forge approve <feature-id> tickets')

  --until   Stop after the named stage (default: tickets)
            wayfinding  - resolve Decisions only
            spec        - also generate/await approval of the Specification
            tickets     - also generate/await approval of the Ticket Plan (default)
`

// runPlan implements `forge plan <feature-id> [--until stage]`, ticket 21's
// unified planning CLI entrypoint. It walks the pipeline stage by stage
// (wayfinding -> spec -> tickets), stopping at whichever comes first: the
// --until bound, an artifact that still needs approval, or a Decision that
// needs human input.
func runPlan(args []string) int {
	featureID, untilStage, code, done := parsePlanArgs(args)
	if done {
		return code
	}

	repoRoot, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge plan: %v\n", err)
		return 1
	}

	cfg, err := loadConfig(defaultConfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge plan: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	store, err := openStore(ctx, defaultDBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge plan: %v\n", err)
		return 1
	}
	defer func() { _ = store.Close() }()

	loader := &fileArtifactLoader{featureID: featureID}

	goalArtifact, err := loader.LoadGoal(ctx, featureID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stdout, "feature %s has no goal.md yet; create .forge/features/%s/goal.md before running forge plan\n", featureID, featureID)
			return 0
		}
		fmt.Fprintf(os.Stderr, "forge plan: load goal: %v\n", err)
		return 1
	}

	decisions, err := loader.LoadDecisions(ctx, featureID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge plan: load decisions: %v\n", err)
		return 1
	}

	specArtifact, err := loader.LoadSpec(ctx, featureID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge plan: load spec: %v\n", err)
		return 1
	}

	// A single Backend is reused across wayfinding, spec generation, and
	// ticket-plan generation within one `forge plan` invocation -- there is
	// no per-stage reason to invoke a fresh one.
	backend, err := buildPlanningBackend(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge plan: %v\n", err)
		return 1
	}

	// Wayfinding: only needed while no spec exists yet -- once a spec has
	// been generated, the Decisions it was derived from are done being
	// resolved for this planning pass (see plan.go's package doc comment on
	// idempotency: an existing artifact is never regenerated).
	if specArtifact == nil {
		if err := verifyTrackerAuth(ctx, cfg, repoRoot); err != nil {
			fmt.Fprintf(os.Stderr, "forge plan: %v\n", err)
			return 1
		}
		trk, err := buildTracker(cfg, repoRoot)
		if err != nil {
			fmt.Fprintf(os.Stderr, "forge plan: %v\n", err)
			return 1
		}

		paused, executionID, err := runWayfindingStage(ctx, store, trk, cfg, backend, repoRoot, featureID, goalArtifact, decisions, loader)
		if err != nil {
			fmt.Fprintf(os.Stderr, "forge plan: %v\n", err)
			return 1
		}
		if paused {
			fmt.Fprintf(os.Stdout, "feature %s is paused on a needs-human Decision; answer it, then run `forge resume %s`\n", featureID, executionID)
			return 0
		}
		fmt.Fprintf(os.Stdout, "wayfinding complete for feature %s\n", featureID)
	} else {
		fmt.Fprintf(os.Stdout, "decisions already resolved for feature %s (spec.md exists); skipping wayfinding\n", featureID)
	}

	if untilStage == "wayfinding" {
		return 0
	}

	facts, err := replan.GatherImplementedFacts(ctx, store, featureID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge plan: gather implemented facts: %v\n", err)
		return 1
	}

	// specEngine is only built (and only then resolves a base revision and
	// compiles the Repository Context, both of which shell out to git) once
	// generation is actually about to happen -- spec.md/ticket-plan.md may
	// already exist and be merely awaiting approval, in which case forge
	// plan does no git-dependent work at all (see the idempotency tests).
	var specEngine *specengine.SpecEngine
	ensureSpecEngine := func() (*specengine.SpecEngine, error) {
		if specEngine != nil {
			return specEngine, nil
		}
		baseRevision, err := resolveBaseRevision(repoRoot, cfg.Git.Base)
		if err != nil {
			return nil, fmt.Errorf("resolve base revision: %w", err)
		}
		specEngine, err = buildSpecEngine(cfg, backend, repoRoot, baseRevision, facts)
		if err != nil {
			return nil, err
		}
		return specEngine, nil
	}

	if specArtifact == nil {
		specEngine, err := ensureSpecEngine()
		if err != nil {
			fmt.Fprintf(os.Stderr, "forge plan: %v\n", err)
			return 1
		}
		if err := specEngine.GenerateSpec(ctx, featureID, loader); err != nil {
			fmt.Fprintf(os.Stderr, "forge plan: %v\n", err)
			return 1
		}
		fmt.Fprintf(os.Stdout, "spec.md generated for feature %s\n", featureID)
		specArtifact, err = loader.LoadSpec(ctx, featureID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "forge plan: reload spec: %v\n", err)
			return 1
		}
	} else {
		fmt.Fprintf(os.Stdout, "spec.md already exists for feature %s; skipping generation\n", featureID)
	}

	if !planning.Approved(specArtifact) {
		fmt.Fprintf(os.Stdout, "spec.md for feature %s awaits approval; run `forge approve %s spec`\n", featureID, featureID)
		return 0
	}

	if untilStage == "spec" {
		return 0
	}

	ticketPlanArtifact, err := loader.LoadTicketPlan(ctx, featureID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge plan: load ticket plan: %v\n", err)
		return 1
	}

	if ticketPlanArtifact == nil {
		specEngine, err := ensureSpecEngine()
		if err != nil {
			fmt.Fprintf(os.Stderr, "forge plan: %v\n", err)
			return 1
		}
		if err := specEngine.GenerateTicketPlan(ctx, featureID, loader); err != nil {
			fmt.Fprintf(os.Stderr, "forge plan: %v\n", err)
			return 1
		}
		fmt.Fprintf(os.Stdout, "ticket-plan.md generated for feature %s\n", featureID)
		ticketPlanArtifact, err = loader.LoadTicketPlan(ctx, featureID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "forge plan: reload ticket plan: %v\n", err)
			return 1
		}
	} else {
		fmt.Fprintf(os.Stdout, "ticket-plan.md already exists for feature %s; skipping generation\n", featureID)
	}

	if !planning.Approved(ticketPlanArtifact) {
		fmt.Fprintf(os.Stdout, "ticket-plan.md for feature %s awaits approval; run `forge approve %s tickets`\n", featureID, featureID)
		return 0
	}

	fmt.Fprintf(os.Stdout, "planning complete for feature %s; run `forge materialize %s`\n", featureID, featureID)
	return 0
}

// buildSpecEngine compiles the full Repository Context for repoRoot via the
// repo-context compiler (ProjectStructure and Languages populated, not just
// BaseRevision) and returns a SpecEngine grounded in it, so the ticket-plan
// (and spec) generation prompts point at the repository's real directories
// and languages instead of guesses.
func buildSpecEngine(cfg config.Config, backend planningagent.Backend, repoRoot, baseRevision string, facts []planningagent.ImplementedFact) (*specengine.SpecEngine, error) {
	repo, err := repocontext.Compile(cfg, repoRoot, baseRevision)
	if err != nil {
		return nil, fmt.Errorf("compile repository context: %w", err)
	}

	engine := specengine.NewSpecEngine(backend)
	engine.Repository = repo
	engine.ImplementedFacts = facts
	return engine, nil
}

// parsePlanArgs parses forge plan's arguments: a single positional
// <feature-id> and an optional --until flag that may appear on either side
// of it. done is true when runPlan should return immediately with code
// (help text, or a parse error).
func parsePlanArgs(args []string) (featureID, untilStage string, code int, done bool) {
	untilStage = "tickets"
	if len(args) == 0 {
		fmt.Fprint(os.Stdout, planUsage)
		return "", "", 0, true
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--help", "-h":
			fmt.Fprint(os.Stdout, planUsage)
			return "", "", 0, true
		case "--until":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "--until requires a stage argument\n\n%s", planUsage)
				return "", "", 1, true
			}
			i++
			untilStage = args[i]
		default:
			if featureID != "" {
				fmt.Fprintf(os.Stderr, "too many arguments: %v\n\n%s", args, planUsage)
				return "", "", 1, true
			}
			featureID = a
		}
	}
	if featureID == "" {
		fmt.Fprintf(os.Stderr, "feature-id is required\n\n%s", planUsage)
		return "", "", 1, true
	}
	switch untilStage {
	case "wayfinding", "spec", "tickets":
	default:
		fmt.Fprintf(os.Stderr, "forge plan: invalid --until value %q (want wayfinding, spec, or tickets)\n\n%s", untilStage, planUsage)
		return "", "", 1, true
	}
	return featureID, untilStage, 0, false
}

// runWayfindingStage runs wayfinding.Loop for featureID under a
// planengine-managed Planning Execution, so a crash mid-loop (or a Decision
// pausing on NEEDS_HUMAN) is resumable exactly the way `forge resume`
// already assumes: the lease survives, and a later `forge plan` (or `forge
// resume`, for the needs-human half) picks the Feature back up from
// whatever Decision state was last persisted. Returns paused=true when the
// loop stopped because a Decision needs human input, along with the
// Planning Execution's ID for the caller to report.
func runWayfindingStage(
	ctx context.Context,
	store storage.Store,
	trk tracker.Tracker,
	cfg config.Config,
	backend planningagent.Backend,
	repoRoot, featureID string,
	goalArtifact *planning.Artifact,
	decisions map[string]*planning.Artifact,
	loader *fileArtifactLoader,
) (paused bool, executionID string, err error) {
	baseRevision, err := resolveBaseRevision(repoRoot, cfg.Git.Base)
	if err != nil {
		return false, "", fmt.Errorf("resolve base revision: %w", err)
	}

	planRuntime := planengine.New(store)
	exec, err := planRuntime.Start(ctx, featureID, baseRevision)
	if err != nil {
		return false, "", fmt.Errorf("start planning execution: %w", err)
	}

	if exec.Status == domain.PlanningStatusNeedsHuman {
		return true, exec.ID, nil
	}

	goalRef := decisiongraph.GoalRef{ID: "goal"}
	if goalArtifact != nil {
		goalRef.Revision = goalArtifact.Revision
	}

	pause := &wayfinding.PauseHandler{
		ExecutionID: exec.ID,
		FeatureID:   featureID,
		Store:       store,
		Tracker:     trk,
		Label:       cfg.Blocked.Label,
		PostComment: cfg.Blocked.Comment,
	}

	persist := wayfinding.Persist(func(id string, artifact *planning.Artifact) error {
		return loader.SaveDecision(ctx, featureID, id, artifact)
	})

	repo, err := repocontext.Compile(cfg, repoRoot, baseRevision)
	if err != nil {
		return false, exec.ID, fmt.Errorf("compile repository context: %w", err)
	}

	if err := wayfinding.Loop(ctx, backend, repo, goalArtifact, goalRef, decisions, persist, pause.Handle); err != nil {
		return false, exec.ID, fmt.Errorf("wayfinding: %w", err)
	}

	finished, err := store.LoadPlanningExecution(ctx, exec.ID)
	if err != nil {
		return false, exec.ID, fmt.Errorf("reload planning execution: %w", err)
	}
	if finished.Status == domain.PlanningStatusNeedsHuman {
		return true, exec.ID, nil
	}

	if err := planRuntime.Finish(ctx, featureID, exec.ID, domain.PlanningStatusComplete); err != nil {
		return false, exec.ID, fmt.Errorf("finish planning execution: %w", err)
	}
	return false, exec.ID, nil
}
