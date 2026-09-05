package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/Teagan42/forge/internal/planengine"
)

// runResume implements `forge resume <execution-id>`: reconcile a persisted
// Execution after an orchestrator restart and continue every incomplete
// Issue from its recorded state. Also resumes Planning Executions that are
// paused on NEEDS_HUMAN (ticket 15b).
func runResume(args []string) int {
	fs := flag.NewFlagSet("forge resume", flag.ContinueOnError)
	configPath := fs.String("config", defaultConfigPath, "path to .forge.yaml")
	dbPath := fs.String("db", defaultDBPath, "path to the SQLite state database")
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

	// First try to resume as a coding execution
	eng, err := buildEngine(store, cfg, repoRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge resume: %v\n", err)
		return 1
	}

	state, err := eng.ResumeExecution(ctx, executionID)
	if err == nil {
		fmt.Printf("execution %s resumed\n", state.Execution.ID)
		for _, issue := range state.Issues {
			fmt.Printf("  issue %s -> %s\n", issue.ID, issue.State)
		}
		return 0
	}

	// If not a coding execution, try as a planning execution
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
}
