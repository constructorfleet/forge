package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/Teagan42/forge/internal/engine"
)

// runStatus implements `forge status <execution-id>`: a pure read of
// whatever `forge execute` (or any other Engine caller) already persisted —
// the Execution, every Issue recorded against it, and its full Event log.
func runStatus(args []string) int {
	fs := flag.NewFlagSet("forge status", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath, "path to the SQLite state database")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "forge status: expected exactly one argument, <execution-id>")
		return 2
	}
	executionID := fs.Arg(0)

	ctx := context.Background()
	store, err := openStore(ctx, *dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge status: %v\n", err)
		return 1
	}
	defer func() { _ = store.Close() }()

	// Status is a pure read: it needs only Store, not the tracker,
	// Workspace manager, or Agent buildEngine would otherwise wire up.
	eng := &engine.Engine{Store: store}
	report, err := eng.Status(ctx, executionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge status: %v\n", err)
		return 1
	}

	printStatus(os.Stdout, report)
	return 0
}

func printStatus(w *os.File, report engine.StatusReport) {
	fmt.Fprintf(w, "execution %s\n", report.Execution.ID)
	fmt.Fprintf(w, "  base:       %s\n", report.Execution.BaseRevision)
	fmt.Fprintf(w, "  started_at: %s\n", report.Execution.StartedAt.Format("2006-01-02T15:04:05Z07:00"))

	fmt.Fprintf(w, "issues (%d):\n", len(report.Issues))
	for _, issue := range report.Issues {
		fmt.Fprintf(w, "  %-12s %-10s scope=%s\n", issue.ID, issue.State, issue.Scope)
	}

	fmt.Fprintf(w, "events (%d):\n", len(report.Events))
	for _, event := range report.Events {
		issueLabel := event.IssueID
		if issueLabel == "" {
			issueLabel = "-"
		}
		fmt.Fprintf(w, "  %s  %-10s issue=%-8s %s\n",
			event.OccurredAt.Format("2006-01-02T15:04:05Z07:00"), event.Type, issueLabel, event.Data)
	}
}
