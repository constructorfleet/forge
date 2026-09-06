package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/planengine"
	"github.com/Teagan42/forge/internal/planningagent"
	"github.com/Teagan42/forge/internal/repolock"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
)

// planGateWait is how long the driver waits between checks while a human gate
// (needs-human or approval) is open. It is well above the ~1s roster poll on
// purpose: a needs-human wait re-fetches the Feature's tracker comments each
// tick (via ResumePlanningExecution), so a short interval would burn the
// tracker's rate budget while a human types. It is a var, not a const, so a
// test can shrink it.
var planGateWait = 3 * time.Second

// planTUIRequest bundles what runPlanTUI needs. It mirrors the fields of
// planPipelineRequest that stay constant across the driver's re-runs; the
// per-run artifacts (goal, decisions, spec) are reloaded each iteration
// because the TUI and `forge approve` mutate them under the driver.
type planTUIRequest struct {
	Store        *storage.SQLiteStore
	PlanRuntime  *planengine.Runtime
	Config       config.Config
	Backend      planningagent.Backend
	RepoRoot     string
	FeatureID    string
	UntilStage   string
	BaseRevision string
	ExecutionID  string
	Loader       *fileArtifactLoader
}

// runPlanTUI drives the whole planning pipeline to completion in one command
// while the PlanningModel renders it. It mirrors runExecuteTUI's shape: the
// TUI runs in a background goroutine as an observer/control surface, the
// driver loop runs on the foreground. The two never share a channel -- the
// SQLite store, the Planning Artifact files, and the tracker are the only
// coordination bus. The TUI mutates that state through its existing Approver
// (writes the artifact approval marker) and Answerer (posts the human's
// decision answer to the tracker); the driver observes the change and re-runs
// runPlanPipeline, which resumes purely from the persisted state.
func runPlanTUI(ctx context.Context, req planTUIRequest) int {
	// Preflight before the TUI takes the screen: a tracker-auth or wiring
	// failure must print a plain error, not corrupt the alternate screen.
	if err := verifyTrackerAuth(ctx, req.Config, req.RepoRoot); err != nil {
		fmt.Fprintf(os.Stderr, "forge plan: %v\n", err)
		return 1
	}
	trk, err := buildTracker(req.Config, req.RepoRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge plan: %v\n", err)
		return 1
	}
	answerer := resolveAnswerer(ctx, req.Config, req.RepoRoot)
	locks := repolock.New(req.RepoRoot)

	model := buildPlanningModelForFeature(req.Store, req.FeatureID, answerer, req.RepoRoot)

	// tuiCtx stops the TUI; driverCtx stops the pipeline. They cancel each
	// other so a human quitting the TUI ends the run, and the run ending
	// closes the TUI -- exactly runExecuteTUI's bidirectional teardown.
	tuiCtx, cancelTUI := context.WithCancel(context.Background())
	defer cancelTUI()
	driverCtx, cancelDriver := context.WithCancel(ctx)
	defer cancelDriver()

	tuiDone := make(chan error, 1)
	go func() {
		modelErr := runPlanningModel(tuiCtx, model)
		cancelDriver() // a human quit (q/Ctrl+C) stops the driver too
		tuiDone <- modelErr
	}()

	stop, driveErr := drivePlanPipeline(driverCtx, req, trk, locks)

	cancelTUI()
	if modelErr := <-tuiDone; modelErr != nil {
		fmt.Fprintf(os.Stderr, "forge plan: %v\n", modelErr)
	}

	// The TUI has released the screen; the real terminal messages print now.
	if driveErr != nil {
		if finishErr := req.PlanRuntime.Finish(context.Background(), req.FeatureID, req.ExecutionID, domain.PlanningStatusFailed); finishErr != nil {
			fmt.Fprintf(os.Stderr, "forge plan: mark planning execution failed: %v\n", finishErr)
		}
		fmt.Fprintf(os.Stderr, "forge plan: %v\n", driveErr)
		return 1
	}
	printPlanTUIOutcome(os.Stdout, req, stop)
	return stop.Code
}

// drivePlanPipeline re-runs runPlanPipeline until planning completes, the
// --until bound is reached, or the human quits. At a human gate it waits for
// the TUI to change the persisted state (an approval marker, or a resumed
// decision), then loops and re-runs. Every re-run reloads the artifacts, so
// it sees the TUI's/approve's edits.
func drivePlanPipeline(ctx context.Context, req planTUIRequest, trk tracker.Tracker, locks *repolock.Locker) (planStop, error) {
	for {
		if ctx.Err() != nil {
			// Human quit before a gate was satisfied. Report the last gate so
			// the outcome message matches the headless resume/approve hint.
			return planStop{Code: 0, Reason: reasonNeedsHuman}, nil
		}

		goalArtifact, err := req.Loader.LoadGoal(ctx, req.FeatureID)
		if err != nil {
			return planStop{}, fmt.Errorf("reload goal: %w", err)
		}
		decisions, err := req.Loader.LoadDecisions(ctx, req.FeatureID)
		if err != nil {
			return planStop{}, fmt.Errorf("reload decisions: %w", err)
		}
		specArtifact, err := req.Loader.LoadSpec(ctx, req.FeatureID)
		if err != nil {
			return planStop{}, fmt.Errorf("reload spec: %w", err)
		}

		stop, err := runPlanPipeline(ctx, planPipelineRequest{
			Store:            req.Store,
			PlanRuntime:      req.PlanRuntime,
			Config:           req.Config,
			Backend:          req.Backend,
			RepoRoot:         req.RepoRoot,
			FeatureID:        req.FeatureID,
			UntilStage:       req.UntilStage,
			BaseRevision:     req.BaseRevision,
			ExecutionID:      req.ExecutionID,
			Goal:             goalArtifact,
			Decisions:        decisions,
			Spec:             specArtifact,
			Loader:           req.Loader,
			Locks:            locks,
			Out:              io.Discard,
			GrillLoadBearing: true,
		})
		if err != nil {
			return planStop{}, err
		}

		switch stop.Reason {
		case reasonComplete, reasonUntilReached:
			return stop, nil
		case reasonNeedsHuman, reasonNeedsApproval:
			if err := waitForGateProgress(ctx, req, trk); err != nil {
				return planStop{}, err
			}
			if ctx.Err() != nil {
				// Quit while at the gate: leave the resting state as-is and
				// report this gate so the outcome hint is accurate.
				return stop, nil
			}
			// State advanced; loop and re-run the pipeline.
		default:
			return stop, nil
		}
	}
}

// gateSignature is the slice of persisted state that tells the driver a human
// gate has been satisfied: the Planning Execution status, the two artifacts'
// approval revisions, and how many decision checkpoints have been resumed.
// Any change means the TUI (or a concurrent `forge approve`/`forge resume`)
// advanced the plan, so the driver should re-run the pipeline.
type gateSignature struct {
	status         domain.PlanningStatus
	specApproved   string
	ticketApproved string
	resumedCount   int
}

// waitForGateProgress blocks until the gate signature changes or the context
// is cancelled. On each tick it calls ResumePlanningExecution, which is a
// no-op unless a new human comment answered a paused decision -- that is how
// an inline decision answer (posted to the tracker by the TUI's Answerer)
// turns into the NEEDS_HUMAN -> ACTIVE transition the driver watches for. An
// approval gate advances through the artifact approval revision instead, with
// no tracker call.
func waitForGateProgress(ctx context.Context, req planTUIRequest, trk tracker.Tracker) error {
	baseline, err := readGateSignature(ctx, req)
	if err != nil {
		return err
	}
	ticker := time.NewTicker(planGateWait)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			// Resume any decision answered on the tracker since the pause.
			// ResumePlanningExecution returns cleanly when the execution is
			// not NEEDS_HUMAN (an approval gate), so this is safe to call for
			// either gate.
			if _, _, err := req.PlanRuntime.ResumePlanningExecution(ctx, req.ExecutionID, trk); err != nil {
				return fmt.Errorf("resume planning execution: %w", err)
			}
			current, err := readGateSignature(ctx, req)
			if err != nil {
				return err
			}
			if current != baseline {
				return nil
			}
		}
	}
}

// readGateSignature reads the current gate signature from the store and the
// artifact files.
func readGateSignature(ctx context.Context, req planTUIRequest) (gateSignature, error) {
	exec, err := req.Store.LoadPlanningExecution(ctx, req.ExecutionID)
	if err != nil {
		return gateSignature{}, fmt.Errorf("load planning execution: %w", err)
	}
	sig := gateSignature{status: exec.Status}

	if spec, err := req.Loader.LoadSpec(ctx, req.FeatureID); err != nil {
		return gateSignature{}, fmt.Errorf("load spec: %w", err)
	} else if spec != nil {
		sig.specApproved = spec.ApprovedRevision
	}

	if tp, err := req.Loader.LoadTicketPlan(ctx, req.FeatureID); err != nil {
		return gateSignature{}, fmt.Errorf("load ticket plan: %w", err)
	} else if tp != nil {
		sig.ticketApproved = tp.ApprovedRevision
	}

	checkpoints, err := req.Store.GetDecisionCheckpointsByExecution(ctx, req.ExecutionID)
	if err != nil {
		return gateSignature{}, fmt.Errorf("load decision checkpoints: %w", err)
	}
	for _, cp := range checkpoints {
		if cp.ResumedAt != nil {
			sig.resumedCount++
		}
	}
	return sig, nil
}

// printPlanTUIOutcome prints the same terminal guidance the headless path
// prints, chosen by the driver's final stop reason, now that the TUI has
// released the screen.
func printPlanTUIOutcome(out io.Writer, req planTUIRequest, stop planStop) {
	switch stop.Reason {
	case reasonComplete:
		fmt.Fprintf(out, "planning complete for feature %s; run `forge materialize %s`\n", req.FeatureID, req.FeatureID)
	case reasonUntilReached:
		fmt.Fprintf(out, "planning stopped at --until %s for feature %s\n", req.UntilStage, req.FeatureID)
	case reasonNeedsHuman:
		fmt.Fprintf(out, "feature %s is paused on a needs-human Decision; re-run `forge plan %s`, or answer it and run `forge resume %s`\n", req.FeatureID, req.FeatureID, req.ExecutionID)
	case reasonNeedsApproval:
		fmt.Fprintf(out, "feature %s awaits approval; re-run `forge plan %s`, or run `forge approve %s`\n", req.FeatureID, req.FeatureID, req.FeatureID)
	}
}
