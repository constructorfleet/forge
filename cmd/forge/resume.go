package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/storage"
)

// runResume implements `forge resume <execution-id>` (ticket 28): manual
// resume for the MVP needs-info flow (see CONTEXT.md's needs-info resume
// flow, issue 07 — daemon/webhook-driven resume is deferred). Today's
// single-issue Execute (ticket 18) means an Execution has exactly one
// Issue, so the argument is the Execution ID `forge execute` printed; the
// one Issue within it currently in NEEDS_INFO is resumed.
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

	store, err := openStore(ctx, *dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge resume: %v\n", err)
		return 1
	}
	defer func() { _ = store.Close() }()

	trk, err := buildTracker(cfg, repoRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge resume: %v\n", err)
		return 1
	}

	issueID, err := findNeedsInfoIssue(ctx, store, executionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge resume: %v\n", err)
		return 1
	}

	result, err := engine.Resume(ctx, store, trk, executionID, issueID, time.Now)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge resume: %v\n", err)
		return 1
	}

	if !result.Resumed {
		fmt.Printf("execution %s: issue %s has no new human input since the needs-info checkpoint; still NEEDS_INFO\n", executionID, issueID)
		return 0
	}

	fmt.Printf("execution %s: issue %s -> %s (%d new comment(s) since checkpoint)\n",
		executionID, issueID, result.Issue.State, len(result.Context.NewComments))
	return 0
}

// findNeedsInfoIssue locates the single Issue within executionID currently
// in NEEDS_INFO, so `forge resume` only needs the Execution ID on the
// command line. Errors if none is found or more than one is (today's
// single-issue Execute can never produce the latter; the check is
// defensive against ticket 26's future multi-issue Executions).
func findNeedsInfoIssue(ctx context.Context, store storage.Store, executionID string) (string, error) {
	issues, err := store.ListIssues(ctx, executionID)
	if err != nil {
		return "", fmt.Errorf("list issues for execution %s: %w", executionID, err)
	}

	var found string
	for _, issue := range issues {
		if issue.State == domain.StateNeedsInfo {
			if found != "" {
				return "", fmt.Errorf("execution %s has more than one Issue in NEEDS_INFO; specify one explicitly (unsupported yet)", executionID)
			}
			found = issue.ID
		}
	}
	if found == "" {
		return "", fmt.Errorf("execution %s has no Issue in NEEDS_INFO", executionID)
	}
	return found, nil
}
