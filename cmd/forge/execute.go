package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

// runExecute implements `forge execute <issue-number>`: the first
// end-to-end vertical slice (ticket 18). It fetches issueID from the
// GitHub tracker, drives it through Forge's state machine via
// internal/engine, and prints the resulting Execution ID and final Issue
// state.
func runExecute(args []string) int {
	fs := flag.NewFlagSet("forge execute", flag.ContinueOnError)
	configPath := fs.String("config", defaultConfigPath, "path to .forge.yaml")
	dbPath := fs.String("db", defaultDBPath, "path to the SQLite state database")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "forge execute: expected exactly one argument, <issue-number>")
		return 2
	}
	issueID := fs.Arg(0)

	repoRoot, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge execute: %v\n", err)
		return 1
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge execute: %v\n", err)
		return 1
	}

	ctx := context.Background()
	store, err := openStore(ctx, *dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge execute: %v\n", err)
		return 1
	}
	defer func() { _ = store.Close() }()

	eng, err := buildEngine(store, cfg, repoRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge execute: %v\n", err)
		return 1
	}

	base, err := resolveBaseRevision(repoRoot, cfg.Git.Base)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge execute: %v\n", err)
		return 1
	}

	result, err := eng.Execute(ctx, issueID, base)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge execute: %v\n", err)
		return 1
	}

	fmt.Printf("execution %s: issue %s -> %s\n", result.ExecutionID, issueID, result.Issue.State)
	return 0
}
