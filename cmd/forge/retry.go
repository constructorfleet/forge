package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/Teagan42/forge/internal/engine"
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

	repoRoot, err := discoverRepoRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge retry: %v\n", err)
		return 1
	}
	if msg, stale := staleBinaryWarning(repoRoot, buildCommit); stale {
		fmt.Fprintln(os.Stderr, msg)
	}

	// --config/--db default to cwd-relative paths (defaultConfigPath,
	// defaultDBPath); an explicit override is used as typed, but the
	// unmodified default is instead resolved against the discovered repo
	// root, so a subdirectory run finds the real .forge.yaml/forge.db
	// rather than a fresh, empty one under the subdirectory.
	explicitConfig, explicitDB := false, false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "config":
			explicitConfig = true
		case "db":
			explicitDB = true
		}
	})
	resolvedConfigPath, resolvedDBPath := *configPath, *dbPath
	if !explicitConfig {
		resolvedConfigPath = filepath.Join(repoRoot, defaultConfigPath)
	}
	if !explicitDB {
		resolvedDBPath = filepath.Join(repoRoot, defaultDBPath)
	}

	cfg, err := loadConfig(resolvedConfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge retry: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := verifyTrackerAuth(ctx, cfg, repoRoot); err != nil {
		fmt.Fprintf(os.Stderr, "forge retry: %v\n", err)
		return 1
	}

	store, err := openStore(ctx, resolvedDBPath)
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
		return reportRetryError(os.Stdout, os.Stderr, executionID, issueID, err)
	}

	fmt.Printf("issue %s/%s -> %s\n", executionID, issueID, issue.State)
	return 0
}

// reportRetryError reports err and returns the exit code for it. A retry
// another actor already claimed is a no-op, not a failure: it prints on out
// and exits 0. Every other error prints on errOut and exits 1.
func reportRetryError(out, errOut io.Writer, executionID, issueID string, err error) int {
	if errors.Is(err, engine.ErrRetryAlreadyClaimed) {
		fmt.Fprintf(out, "issue %s/%s: another actor already claimed this retry; nothing to do\n", executionID, issueID)
		return 0
	}
	fmt.Fprintf(errOut, "forge retry: %v\n", err)
	return 1
}

func parseIssueExecutionID(value string) (executionID, issueID string, err error) {
	executionID, issueID, ok := strings.Cut(value, "/")
	if !ok || executionID == "" || issueID == "" {
		return "", "", fmt.Errorf("expected <execution-id>/<issue-id>, got %q", value)
	}
	return executionID, issueID, nil
}
