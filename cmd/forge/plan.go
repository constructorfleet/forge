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
	// no per-stage reason to invoke a fresh one. It is also the one seam
	// every stage's agent invocation passes through, so handing it the
	// Store here is what gets planning transcripts into transcript_events
	// (issue #248); see buildPlanningBackend for the Feature-scoped keying.
	backend, err := buildPlanningBackend(cfg, store, featureID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge plan: %v\n", err)
		return 1
	}

	// baseRevision is resolved here, unconditionally, because Start (below)
	// must create a planning_executions row for every `forge plan`
	// invocation now, including the fully-idempotent no-op case where every
	// artifact already exists and is approved (issue #470's fix requires a
	// live row even then, so an observer can always find one). A resolvable
	// git.base is therefore a hard requirement for every `forge plan`
	// invocation; this replaces the narrower guarantee that used to hold
	// here, that an idempotent no-op run did no git-dependent work at all.
	baseRevision, err := resolveBaseRevision(repoRoot, cfg.Git.Base)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge plan: resolve base revision: %v\n", err)
		return 1
	}

	// The Planning Execution is started here, before any stage runs, so the
	// planning_executions row and feature_planning_leases claim span the
	// whole pipeline (wayfinding through ticket-plan review) rather than
	// only wayfinding (issue #470). Every return below this point that
	// leaves the pipeline unfinished must leave exec's row in a resting,
	// non-terminal Status (ACTIVE, NEEDS_APPROVAL, or NEEDS_HUMAN -- the
	// wayfinding pause path sets NEEDS_HUMAN itself, via
	// wayfinding.PauseHandler.Handle) with the lease still held, so a later
	// `forge plan` reclaims the same execution instead of starting a fresh
	// one; only a hard failure or the pipeline's actual completion may call
	// planRuntime.Finish.
	planRuntime := planengine.New(store)
	exec, err := planRuntime.Start(ctx, featureID, baseRevision)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge plan: %v\n", err)
		return 1
	}

	if exec.Status == domain.PlanningStatusNeedsHuman {
		fmt.Fprintf(os.Stdout, "feature %s is paused on a needs-human Decision; answer it, then run `forge resume %s`\n", featureID, exec.ID)
		return 0
	}

	code, err = runPlanPipeline(ctx, planPipelineRequest{
		Store:        store,
		PlanRuntime:  planRuntime,
		Config:       cfg,
		Backend:      backend,
		RepoRoot:     repoRoot,
		FeatureID:    featureID,
		UntilStage:   untilStage,
		BaseRevision: baseRevision,
		ExecutionID:  exec.ID,
		Goal:         goalArtifact,
		Decisions:    decisions,
		Spec:         specArtifact,
		Loader:       loader,
	})
	if err != nil {
		if finishErr := planRuntime.Finish(ctx, featureID, exec.ID, domain.PlanningStatusFailed); finishErr != nil {
			fmt.Fprintf(os.Stderr, "forge plan: mark planning execution failed: %v\n", finishErr)
		}
		fmt.Fprintf(os.Stderr, "forge plan: %v\n", err)
		return 1
	}
	return code
}

// planPipelineRequest bundles the per-run values runPlanPipeline needs, so
// same-typed fields (RepoRoot, FeatureID, UntilStage, BaseRevision,
// ExecutionID are all strings) are addressed by name rather than position.
type planPipelineRequest struct {
	Store       storage.Store
	PlanRuntime *planengine.Runtime
	Config      config.Config
	Backend     planningagent.Backend

	RepoRoot     string
	FeatureID    string
	UntilStage   string
	BaseRevision string
	ExecutionID  string

	Goal      *planning.Artifact
	Decisions map[string]*planning.Artifact
	Spec      *planning.Artifact
	Loader    *fileArtifactLoader
}

// runPlanPipeline runs the stages of `forge plan` under the Planning
// Execution req.ExecutionID that runPlan already started: wayfinding (unless
// spec.md already exists), spec generation/approval, then ticket-plan
// generation/approval, honoring the --until bound at each stage boundary.
// It returns the process exit code for every non-error stopping point
// (a human gate, an approval gate, --until, or full completion); the caller
// is responsible for marking req.ExecutionID FAILED when it returns an
// error.
func runPlanPipeline(ctx context.Context, req planPipelineRequest) (int, error) {
	store := req.Store
	planRuntime := req.PlanRuntime
	cfg := req.Config
	backend := req.Backend
	repoRoot := req.RepoRoot
	featureID := req.FeatureID
	untilStage := req.UntilStage
	baseRevision := req.BaseRevision
	executionID := req.ExecutionID
	goalArtifact := req.Goal
	decisions := req.Decisions
	specArtifact := req.Spec
	loader := req.Loader

	// Wayfinding: only needed while no spec exists yet -- once a spec has
	// been generated, the Decisions it was derived from are done being
	// resolved for this planning pass (see plan.go's package doc comment on
	// idempotency: an existing artifact is never regenerated).
	if specArtifact == nil {
		if err := verifyTrackerAuth(ctx, cfg, repoRoot); err != nil {
			return 0, err
		}
		trk, err := buildTracker(cfg, repoRoot)
		if err != nil {
			return 0, err
		}

		paused, err := runWayfindingStage(ctx, store, trk, cfg, backend, repoRoot, featureID, baseRevision, executionID, goalArtifact, decisions, loader)
		if err != nil {
			return 0, err
		}
		if paused {
			fmt.Fprintf(os.Stdout, "feature %s is paused on a needs-human Decision; answer it, then run `forge resume %s`\n", featureID, executionID)
			return 0, nil
		}
		fmt.Fprintf(os.Stdout, "wayfinding complete for feature %s\n", featureID)
	} else {
		fmt.Fprintf(os.Stdout, "decisions already resolved for feature %s (spec.md exists); skipping wayfinding\n", featureID)
	}

	if untilStage == "wayfinding" {
		return 0, nil
	}

	facts, err := replan.GatherImplementedFacts(ctx, store, featureID)
	if err != nil {
		return 0, fmt.Errorf("gather implemented facts: %w", err)
	}

	// specEngine is only built (and only then compiles the Repository
	// Context, which shells out to git) once generation is actually about
	// to happen -- spec.md/ticket-plan.md may already exist and be merely
	// awaiting approval, in which case forge plan does no further
	// git-dependent work at all (see the idempotency tests).
	var specEngine *specengine.SpecEngine
	ensureSpecEngine := func() (*specengine.SpecEngine, error) {
		if specEngine != nil {
			return specEngine, nil
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
			return 0, err
		}
		if err := specEngine.GenerateSpec(ctx, featureID, loader); err != nil {
			return 0, err
		}
		fmt.Fprintf(os.Stdout, "spec.md generated for feature %s\n", featureID)
		specArtifact, err = loader.LoadSpec(ctx, featureID)
		if err != nil {
			return 0, fmt.Errorf("reload spec: %w", err)
		}
	} else {
		fmt.Fprintf(os.Stdout, "spec.md already exists for feature %s; skipping generation\n", featureID)
	}

	if !planning.Approved(specArtifact) {
		return markAwaitingApproval(ctx, store, executionID, featureID, "spec", "spec.md")
	}

	if untilStage == "spec" {
		return 0, nil
	}

	ticketPlanArtifact, err := loader.LoadTicketPlan(ctx, featureID)
	if err != nil {
		return 0, fmt.Errorf("load ticket plan: %w", err)
	}

	if ticketPlanArtifact == nil {
		specEngine, err := ensureSpecEngine()
		if err != nil {
			return 0, err
		}
		if err := specEngine.GenerateTicketPlan(ctx, featureID, loader); err != nil {
			return 0, err
		}
		fmt.Fprintf(os.Stdout, "ticket-plan.md generated for feature %s\n", featureID)
		ticketPlanArtifact, err = loader.LoadTicketPlan(ctx, featureID)
		if err != nil {
			return 0, fmt.Errorf("reload ticket plan: %w", err)
		}
	} else {
		fmt.Fprintf(os.Stdout, "ticket-plan.md already exists for feature %s; skipping generation\n", featureID)
	}

	if !planning.Approved(ticketPlanArtifact) {
		return markAwaitingApproval(ctx, store, executionID, featureID, "tickets", "ticket-plan.md")
	}

	if err := planRuntime.Finish(ctx, featureID, executionID, domain.PlanningStatusComplete); err != nil {
		return 0, fmt.Errorf("finish planning execution: %w", err)
	}
	fmt.Fprintf(os.Stdout, "planning complete for feature %s; run `forge materialize %s`\n", featureID, featureID)
	return 0, nil
}

// markAwaitingApproval records executionID's Planning Execution as
// NEEDS_APPROVAL and reports the artifact's approval command to the user.
// approveArg is the `forge approve <feature-id> <approveArg>` stage name
// (e.g. "spec" or "tickets"); artifactName is the file the message names
// (e.g. "spec.md" or "ticket-plan.md").
func markAwaitingApproval(ctx context.Context, store storage.Store, executionID, featureID, approveArg, artifactName string) (int, error) {
	if err := store.UpdatePlanningStatus(ctx, executionID, domain.PlanningStatusNeedsApproval); err != nil {
		return 0, fmt.Errorf("mark planning execution awaiting %s approval: %w", approveArg, err)
	}
	fmt.Fprintf(os.Stdout, "%s for feature %s awaits approval; run `forge approve %s %s`\n", artifactName, featureID, featureID, approveArg)
	return 0, nil
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

// runWayfindingStage runs wayfinding.Loop for featureID under the Planning
// Execution executionID that runPlan already started, so a crash mid-loop
// (or a Decision pausing on NEEDS_HUMAN) is resumable exactly the way
// `forge resume` already assumes: the lease survives, and a later `forge
// plan` (or `forge resume`, for the needs-human half) picks the Feature
// back up from whatever Decision state was last persisted. Returns
// paused=true when the loop stopped because a Decision needs human input;
// the caller is responsible for the Planning Execution's Start/Finish, not
// this stage.
func runWayfindingStage(
	ctx context.Context,
	store storage.Store,
	trk tracker.Tracker,
	cfg config.Config,
	backend planningagent.Backend,
	repoRoot, featureID, baseRevision, executionID string,
	goalArtifact *planning.Artifact,
	decisions map[string]*planning.Artifact,
	loader *fileArtifactLoader,
) (paused bool, err error) {
	goalRef := decisiongraph.GoalRef{ID: "goal"}
	if goalArtifact != nil {
		goalRef.Revision = goalArtifact.Revision
	}

	pause := &wayfinding.PauseHandler{
		ExecutionID: executionID,
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
		return false, fmt.Errorf("compile repository context: %w", err)
	}

	if err := wayfinding.Loop(ctx, backend, repo, goalArtifact, goalRef, decisions, persist, pause.Handle); err != nil {
		return false, fmt.Errorf("wayfinding: %w", err)
	}

	finished, err := store.LoadPlanningExecution(ctx, executionID)
	if err != nil {
		return false, fmt.Errorf("reload planning execution: %w", err)
	}
	return finished.Status == domain.PlanningStatusNeedsHuman, nil
}
