package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/decisiongraph"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/planengine"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningagent"
	"github.com/Teagan42/forge/internal/replan"
	"github.com/Teagan42/forge/internal/repocontext"
	"github.com/Teagan42/forge/internal/repolock"
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

  --tui     Force the interactive planning TUI on. It renders the live
            planning transcript and grills / awaits approval inline, driving
            the whole pipeline to completion in one command. Default on a TTY.
  --no-tui  Force the headless path: print each stage's status and stop at
            each human gate (for scripts and AFK runs).
`

// planStopReason names why runPlanPipeline stopped, so a caller can react
// without parsing printed text. The interactive driver (runPlanTUI) uses it
// to tell a human gate (NeedsHuman, NeedsApproval) apart from a real end
// (Complete, UntilReached).
type planStopReason int

const (
	// reasonComplete: the pipeline reached the end and marked the Planning
	// Execution COMPLETE.
	reasonComplete planStopReason = iota
	// reasonNeedsHuman: wayfinding paused on a Decision that needs human input.
	reasonNeedsHuman
	// reasonNeedsApproval: an artifact (spec.md or ticket-plan.md) awaits
	// approval.
	reasonNeedsApproval
	// reasonUntilReached: the --until bound stopped the pipeline before a gate.
	reasonUntilReached
)

// planStop is runPlanPipeline's resting point: the process exit code for the
// headless path, plus the Reason the interactive driver switches on.
type planStop struct {
	Code   int
	Reason planStopReason
}

// runPlan implements `forge plan <feature-id> [--until stage]`, ticket 21's
// unified planning CLI entrypoint. It walks the pipeline stage by stage
// (wayfinding -> spec -> tickets), stopping at whichever comes first: the
// --until bound, an artifact that still needs approval, or a Decision that
// needs human input.
func runPlan(args []string) int {
	featureID, untilStage, tuiFlag, code, done := parsePlanArgs(args)
	if done {
		return code
	}
	tuiVal, tuiSet := tuiFlag.wasSet()
	useTUI := shouldUseTUI(tuiVal, tuiSet, isTerminalSession())

	repoRoot, err := discoverRepoRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge plan: %v\n", err)
		return 1
	}

	// forge plan has no --config/--db flags to override (unlike execute,
	// resume, cancel, and retry), so its defaults always resolve against the
	// discovered repo root rather than the process's own working directory
	// (issue #576, the same fix issue #459 made for `forge retry`).
	cfg, err := loadConfig(filepath.Join(repoRoot, defaultConfigPath))
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge plan: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	store, err := openStore(ctx, filepath.Join(repoRoot, defaultDBPath))
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge plan: %v\n", err)
		return 1
	}
	defer func() { _ = store.Close() }()

	loader := &fileArtifactLoader{RepoRoot: repoRoot}

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

	if useTUI {
		// The interactive driver renders the planning TUI and drives every
		// gate (needs-human, approval) to completion in one command. It never
		// takes the "run `forge resume`" shortcut below: a NEEDS_HUMAN resting
		// state from a prior run is resumed inline by the driver's own loop.
		return runPlanTUI(ctx, planTUIRequest{
			Store:        store,
			PlanRuntime:  planRuntime,
			Config:       cfg,
			Backend:      backend,
			RepoRoot:     repoRoot,
			FeatureID:    featureID,
			UntilStage:   untilStage,
			BaseRevision: baseRevision,
			ExecutionID:  exec.ID,
			Loader:       loader,
		})
	}

	if exec.Status == domain.PlanningStatusNeedsHuman {
		fmt.Fprintf(os.Stdout, "feature %s is paused on a needs-human Decision; answer it, then run `forge resume %s`\n", featureID, exec.ID)
		return 0
	}

	// A tracker-auth failure is a warning, not a stop: planning is local-first,
	// so a needs-human pause records the answer locally and needs no tracker.
	if err := verifyTrackerAuth(ctx, cfg, repoRoot); err != nil {
		fmt.Fprintf(os.Stderr, "forge plan: tracker unavailable, continuing with local planning: %v\n", err)
	}

	pstop, err := runPlanPipeline(ctx, planPipelineRequest{
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
		Locks:        repolock.New(repoRoot),
	})
	if err != nil {
		if finishErr := planRuntime.Finish(ctx, featureID, exec.ID, domain.PlanningStatusFailed); finishErr != nil {
			fmt.Fprintf(os.Stderr, "forge plan: mark planning execution failed: %v\n", finishErr)
		}
		fmt.Fprintf(os.Stderr, "forge plan: %v\n", err)
		return 1
	}
	return pstop.Code
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

	// Locks guards spec.md/ticket-plan.md generation against a concurrent
	// `forge approve`, which mutates the same files under the same
	// "planning:<feature-id>" resource name (internal/planningapprove).
	// Without this, the two processes' writes race with no serialization.
	Locks *repolock.Locker

	// Out receives the per-stage status lines. A nil Out defaults to
	// os.Stdout (the headless path). The interactive driver passes io.Discard
	// so the lines never corrupt the TUI's alternate screen.
	Out io.Writer

	// GrillLoadBearing makes wayfinding defer load-bearing Decisions to the
	// human instead of resolving them autonomously. The interactive driver
	// sets it; the headless path leaves it false.
	GrillLoadBearing bool
}

// runPlanPipeline runs the stages of `forge plan` under the Planning
// Execution req.ExecutionID that runPlan already started: wayfinding (unless
// spec.md already exists), spec generation/approval, then ticket-plan
// generation/approval, honoring the --until bound at each stage boundary.
// It returns a planStop for every non-error stopping point (a human gate, an
// approval gate, --until, or full completion): planStop.Code is the process
// exit code for the headless path, and planStop.Reason lets the interactive
// driver tell a human gate apart from a real end. The caller is responsible
// for marking req.ExecutionID FAILED when it returns an error.
func runPlanPipeline(ctx context.Context, req planPipelineRequest) (planStop, error) {
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
	out := req.Out
	if out == nil {
		out = os.Stdout
	}

	// Wayfinding: only needed while no spec exists yet -- once a spec has
	// been generated, the Decisions it was derived from are done being
	// resolved for this planning pass (see plan.go's package doc comment on
	// idempotency: an existing artifact is never regenerated).
	if specArtifact == nil {
		// No tracker-auth preflight here: planning is local-first (see
		// runPlanTUI), so a needs-human pause records the answer locally and
		// needs no tracker. Only a Feature that is itself a tracker issue uses
		// the tracker, and every such call degrades on its own. The command
		// entry points warn once, up front, if auth fails; this shared path
		// must not do user I/O, since the TUI driver runs it under the
		// alternate screen. buildTracker is config-only and stays a hard error.
		trk, err := buildTracker(cfg, repoRoot)
		if err != nil {
			return planStop{}, err
		}

		paused, err := runWayfindingStage(ctx, store, trk, cfg, backend, repoRoot, featureID, baseRevision, executionID, goalArtifact, decisions, loader, req.GrillLoadBearing)
		if err != nil {
			return planStop{}, err
		}
		if paused {
			fmt.Fprintf(out, "feature %s is paused on a needs-human Decision; answer it, then run `forge resume %s`\n", featureID, executionID)
			return planStop{Code: 0, Reason: reasonNeedsHuman}, nil
		}
		fmt.Fprintf(out, "wayfinding complete for feature %s\n", featureID)
	} else {
		fmt.Fprintf(out, "decisions already resolved for feature %s (spec.md exists); skipping wayfinding\n", featureID)
	}

	if untilStage == "wayfinding" {
		return planStop{Code: 0, Reason: reasonUntilReached}, nil
	}

	facts, err := replan.GatherImplementedFacts(ctx, store, featureID)
	if err != nil {
		return planStop{}, fmt.Errorf("gather implemented facts: %w", err)
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
			return planStop{}, err
		}
		if err := req.Locks.WithLock(ctx, "planning:"+featureID, func() error {
			return specEngine.GenerateSpec(ctx, featureID, loader)
		}); err != nil {
			return planStop{}, err
		}
		fmt.Fprintf(out, "spec.md generated for feature %s\n", featureID)
		specArtifact, err = loader.LoadSpec(ctx, featureID)
		if err != nil {
			return planStop{}, fmt.Errorf("reload spec: %w", err)
		}
	} else {
		fmt.Fprintf(out, "spec.md already exists for feature %s; skipping generation\n", featureID)
	}

	if !planning.Approved(specArtifact) {
		return markAwaitingApproval(ctx, out, store, executionID, featureID, "spec", "spec.md")
	}

	if untilStage == "spec" {
		return planStop{Code: 0, Reason: reasonUntilReached}, nil
	}

	ticketPlanArtifact, err := loader.LoadTicketPlan(ctx, featureID)
	if err != nil {
		return planStop{}, fmt.Errorf("load ticket plan: %w", err)
	}

	if ticketPlanArtifact == nil {
		specEngine, err := ensureSpecEngine()
		if err != nil {
			return planStop{}, err
		}
		if err := req.Locks.WithLock(ctx, "planning:"+featureID, func() error {
			return specEngine.GenerateTicketPlan(ctx, featureID, loader)
		}); err != nil {
			return planStop{}, err
		}
		fmt.Fprintf(out, "ticket-plan.md generated for feature %s\n", featureID)
		ticketPlanArtifact, err = loader.LoadTicketPlan(ctx, featureID)
		if err != nil {
			return planStop{}, fmt.Errorf("reload ticket plan: %w", err)
		}
	} else {
		fmt.Fprintf(out, "ticket-plan.md already exists for feature %s; skipping generation\n", featureID)
	}

	if !planning.Approved(ticketPlanArtifact) {
		return markAwaitingApproval(ctx, out, store, executionID, featureID, "tickets", "ticket-plan.md")
	}

	if err := planRuntime.Finish(ctx, featureID, executionID, domain.PlanningStatusComplete); err != nil {
		return planStop{}, fmt.Errorf("finish planning execution: %w", err)
	}
	fmt.Fprintf(out, "planning complete for feature %s; run `forge materialize %s`\n", featureID, featureID)
	return planStop{Code: 0, Reason: reasonComplete}, nil
}

// markAwaitingApproval records executionID's Planning Execution as
// NEEDS_APPROVAL and reports the artifact's approval command to the user.
// approveArg is the `forge approve <feature-id> <approveArg>` stage name
// (e.g. "spec" or "tickets"); artifactName is the file the message names
// (e.g. "spec.md" or "ticket-plan.md").
func markAwaitingApproval(ctx context.Context, out io.Writer, store storage.Store, executionID, featureID, approveArg, artifactName string) (planStop, error) {
	if err := store.UpdatePlanningStatus(ctx, executionID, domain.PlanningStatusNeedsApproval); err != nil {
		return planStop{}, fmt.Errorf("mark planning execution awaiting %s approval: %w", approveArg, err)
	}
	fmt.Fprintf(out, "%s for feature %s awaits approval; run `forge approve %s %s`\n", artifactName, featureID, featureID, approveArg)
	return planStop{Code: 0, Reason: reasonNeedsApproval}, nil
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
func parsePlanArgs(args []string) (featureID, untilStage string, tui triState, code int, done bool) {
	untilStage = "tickets"
	if len(args) == 0 {
		fmt.Fprint(os.Stdout, planUsage)
		return "", "", triState{}, 0, true
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--help", "-h":
			fmt.Fprint(os.Stdout, planUsage)
			return "", "", triState{}, 0, true
		case "--until":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "--until requires a stage argument\n\n%s", planUsage)
				return "", "", triState{}, 1, true
			}
			i++
			untilStage = args[i]
		case "--tui":
			// Bare flag: force the planning TUI on. Mirrors forge execute's
			// --tui, but forge plan parses arguments by hand, so the flag is
			// matched here rather than via flag.BoolFunc.
			tui.set, tui.val = true, true
		case "--no-tui":
			tui.set, tui.val = true, false
		default:
			if featureID != "" {
				fmt.Fprintf(os.Stderr, "too many arguments: %v\n\n%s", args, planUsage)
				return "", "", triState{}, 1, true
			}
			featureID = a
		}
	}
	if featureID == "" {
		fmt.Fprintf(os.Stderr, "feature-id is required\n\n%s", planUsage)
		return "", "", triState{}, 1, true
	}
	switch untilStage {
	case "wayfinding", "spec", "tickets":
	default:
		fmt.Fprintf(os.Stderr, "forge plan: invalid --until value %q (want wayfinding, spec, or tickets)\n\n%s", untilStage, planUsage)
		return "", "", triState{}, 1, true
	}
	return featureID, untilStage, tui, 0, false
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
	grillLoadBearing bool,
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

	// A Decision the human already answered (resume path) is re-opened and
	// re-resolved from their words: gather the answers, then clear the paused
	// State on each still-paused answered Decision so Frontier puts it back on
	// the frontier. The resolver reads the answer from HumanInputs and records
	// a real Outcome instead of deferring again.
	humanInputs, err := gatherResumedAnswers(ctx, store, executionID)
	if err != nil {
		return false, err
	}
	for id, d := range decisions {
		if humanInputs[id] != "" && d.State == decisiongraph.StateNeedsHuman {
			d.State = ""
		}
	}

	if err := wayfinding.Loop(ctx, backend, repo, goalArtifact, goalRef, decisions, persist, pause.Handle,
		wayfinding.WithHumanInputs(humanInputs), wayfinding.WithGrillLoadBearing(grillLoadBearing)); err != nil {
		return false, fmt.Errorf("wayfinding: %w", err)
	}

	finished, err := store.LoadPlanningExecution(ctx, executionID)
	if err != nil {
		return false, fmt.Errorf("reload planning execution: %w", err)
	}
	return finished.Status == domain.PlanningStatusNeedsHuman, nil
}

// gatherResumedAnswers reads every resumed Decision checkpoint for executionID
// and returns each Decision's human answer, keyed by Decision ID. A resumed
// checkpoint carries the human comments ResumeDecision detected; the answer is
// those comment bodies joined. The wayfinding Loop feeds these back into
// re-resolution so an answered Decision resolves from the human's words rather
// than deferring again.
func gatherResumedAnswers(ctx context.Context, store storage.Store, executionID string) (map[string]string, error) {
	checkpoints, err := store.GetDecisionCheckpointsByExecution(ctx, executionID)
	if err != nil {
		return nil, fmt.Errorf("load decision checkpoints: %w", err)
	}
	answers := map[string]string{}
	for _, cp := range checkpoints {
		if cp.ResumedAt == nil || cp.ResumedContext == "" {
			continue
		}
		var rc wayfinding.ResumedDecisionContext
		if err := json.Unmarshal([]byte(cp.ResumedContext), &rc); err != nil {
			return nil, fmt.Errorf("parse resumed context for decision %s: %w", cp.DecisionID, err)
		}
		var parts []string
		for _, c := range rc.NewComments {
			if body := strings.TrimSpace(c.Body); body != "" {
				parts = append(parts, body)
			}
		}
		if len(parts) > 0 {
			answers[cp.DecisionID] = strings.Join(parts, "\n\n")
		}
	}
	return answers, nil
}
