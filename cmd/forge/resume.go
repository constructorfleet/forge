package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/Teagan42/forge/internal/planengine"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/wayfinding"
)

// runResume implements `forge resume <execution-id>`: reconcile a persisted
// Execution after an orchestrator restart and continue every incomplete
// Issue from its recorded state. Also resumes Planning Executions that are
// paused on NEEDS_HUMAN (ticket 15b).
func runResume(args []string) int {
	fs := flag.NewFlagSet("forge resume", flag.ContinueOnError)
	configPath := fs.String("config", defaultConfigPath, "path to .forge.yaml")
	dbPath := fs.String("db", defaultDBPath, "path to the SQLite state database")
	answer := fs.String("answer", "", "answer a paused needs-human Decision locally, without a tracker (for a local Feature slug)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "forge resume: expected exactly one argument, <execution-id>")
		return 2
	}
	executionID := fs.Arg(0)

	repoRoot, err := discoverRepoRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge resume: %v\n", err)
		return 1
	}
	if msg, stale := staleBinaryWarning(repoRoot, buildCommit); stale {
		fmt.Fprintln(os.Stderr, msg)
	}

	resolvedConfigPath, resolvedDBPath := resolveConfigDBPaths(fs, repoRoot, *configPath, *dbPath)

	cfg, err := loadConfig(resolvedConfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge resume: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := verifyTrackerAuth(ctx, cfg, repoRoot); err != nil {
		fmt.Fprintf(os.Stderr, "forge resume: %v\n", err)
		return 1
	}

	store, err := openStore(ctx, resolvedDBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge resume: %v\n", err)
		return 1
	}
	defer func() { _ = store.Close() }()
	defer clearOwnedWorkerClaims(context.Background(), store)

	// The two ID spaces (Executions and Planning Executions) are both
	// UUIDs, so shape cannot tell them apart. Probe each table explicitly
	// and dispatch on what exists, instead of using a resume error as a
	// signal to try the other space: a real failure resuming an Execution
	// must be reported as itself, not masked by an unrelated
	// planning-resume error about an ID that was never a Feature.
	_, execLookupErr := store.LoadExecution(ctx, executionID)
	_, planLookupErr := store.LoadPlanningExecution(ctx, executionID)

	switch {
	case execLookupErr == nil:
		eng, err := buildEngine(store, cfg, repoRoot)
		if err != nil {
			fmt.Fprintf(os.Stderr, "forge resume: %v\n", err)
			return 1
		}
		state, err := eng.ResumeExecution(ctx, executionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "forge resume: %v\n", err)
			return 1
		}
		fmt.Printf("execution %s resumed\n", state.Execution.ID)
		for _, issue := range state.Issues {
			fmt.Printf("  issue %s -> %s\n", issue.ID, issue.State)
		}
		return 0
	case planLookupErr == nil:
		// A local Feature slug has no tracker issue, so a human answers a
		// paused Decision through --answer, which records the answer locally
		// and transitions the execution to ACTIVE without a tracker.
		if *answer != "" {
			return resumeWithLocalAnswer(ctx, store, executionID, *answer)
		}
		trk, err := buildTracker(cfg, repoRoot)
		if err != nil {
			fmt.Fprintf(os.Stderr, "forge resume: %v\n", err)
			return 1
		}
		planRuntime := planengine.New(store)
		planRuntime.Now = func() time.Time { return time.Now().UTC() }
		exec, resumed, err := planRuntime.ResumePlanningExecution(ctx, executionID, trk)
		if err != nil {
			fmt.Fprintf(os.Stderr, "forge resume: %v\n", err)
			return 1
		}
		if resumed {
			fmt.Printf("planning execution %s resumed (status: %s)\n", exec.ID, exec.Status)
		} else {
			fmt.Printf("planning execution %s has no new human input (status: %s)\n", exec.ID, exec.Status)
		}
		return 0
	case errors.Is(execLookupErr, storage.ErrNotFound) && errors.Is(planLookupErr, storage.ErrNotFound):
		fmt.Fprintf(os.Stderr, "forge resume: no execution or planning execution found for id %s\n", executionID)
		return 1
	case !errors.Is(execLookupErr, storage.ErrNotFound):
		fmt.Fprintf(os.Stderr, "forge resume: %v\n", execLookupErr)
		return 1
	default:
		fmt.Fprintf(os.Stderr, "forge resume: %v\n", planLookupErr)
		return 1
	}
}

// resumeWithLocalAnswer answers the single paused needs-human Decision of a
// Planning Execution locally, with no tracker. It is the headless counterpart
// to the planning TUI's answer key for a local Feature slug. Planning is
// strictly sequential, so at most one Decision is ever paused at a time.
func resumeWithLocalAnswer(ctx context.Context, store *storage.SQLiteStore, executionID, answer string) int {
	checkpoints, err := store.GetDecisionCheckpointsByExecution(ctx, executionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge resume: %v\n", err)
		return 1
	}
	var pending []storage.DecisionCheckpoint
	for _, cp := range checkpoints {
		if cp.ResumedAt == nil {
			pending = append(pending, cp)
		}
	}
	if len(pending) == 0 {
		fmt.Fprintf(os.Stderr, "forge resume: no paused needs-human decision to answer for planning execution %s\n", executionID)
		return 1
	}
	if len(pending) > 1 {
		fmt.Fprintf(os.Stderr, "forge resume: found %d paused decisions for planning execution %s; expected at most one\n", len(pending), executionID)
		return 1
	}

	now := func() time.Time { return time.Now().UTC() }
	if _, err := wayfinding.AnswerDecisionLocally(ctx, store, executionID, pending[0].DecisionID, answer, now); err != nil {
		fmt.Fprintf(os.Stderr, "forge resume: %v\n", err)
		return 1
	}
	fmt.Printf("planning execution %s resumed with a local answer to decision %s; re-run `forge plan` to continue\n", executionID, pending[0].DecisionID)
	return 0
}
