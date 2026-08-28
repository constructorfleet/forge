package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
)

func runRetry(args []string) int {
	fs := flag.NewFlagSet("forge retry", flag.ContinueOnError)
	configPath := fs.String("config", defaultConfigPath, "path to .forge.yaml")
	dbPath := fs.String("db", defaultDBPath, "path to the SQLite state database")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "forge retry: expected exactly one argument, <execution-id>/<issue-id>")
		return 2
	}
	executionID, issueID, err := parseIssueExecutionID(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge retry: %v\n", err)
		return 2
	}

	repoRoot, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge retry: %v\n", err)
		return 1
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge retry: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	store, err := openStore(ctx, *dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge retry: %v\n", err)
		return 1
	}
	defer func() { _ = store.Close() }()

	eng, err := buildEngine(store, cfg, repoRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge retry: %v\n", err)
		return 1
	}
	issue, err := eng.RetryIssue(ctx, executionID, issueID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge retry: %v\n", err)
		return 1
	}

	fmt.Printf("issue %s/%s -> %s\n", executionID, issueID, issue.State)
	return 0
}

func parseIssueExecutionID(value string) (executionID, issueID string, err error) {
	executionID, issueID, ok := strings.Cut(value, "/")
	if !ok || executionID == "" || issueID == "" {
		return "", "", fmt.Errorf("expected <execution-id>/<issue-id>, got %q", value)
	}
	return executionID, issueID, nil
}
