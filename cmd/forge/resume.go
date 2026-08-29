package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
)

// runResume implements `forge resume <execution-id>`: reconcile a persisted
// Execution after an orchestrator restart and continue every incomplete
// Issue from its recorded state.
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

	repoRoot, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge resume: %v\n", err)
		return 1
	}

	cfg, err := loadConfig(*configPath)
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

	store, err := openStore(ctx, *dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge resume: %v\n", err)
		return 1
	}
	defer func() { _ = store.Close() }()

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
}
