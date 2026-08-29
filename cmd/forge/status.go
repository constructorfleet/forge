package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/storage"
)

// runStatus implements `forge status [execution-id] [issue-id]`: a pure
// read of whatever `forge execute` (or any other Engine caller) already
// persisted — the Execution, every Issue recorded against it, its full
// Event log, and (with --transcript) one Issue's Agent transcript across
// every attempt (ticket 28).
func runStatus(args []string) int {
	fs := flag.NewFlagSet("forge status", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath, "path to the SQLite state database")
	transcript := fs.Bool("transcript", false, "print the Agent transcript for <execution-id> <issue-id> instead of status")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 2 {
		fmt.Fprintln(os.Stderr, "forge status: expected zero, one, or two arguments, [execution-id] [issue-id]")
		return 2
	}
	if *transcript && fs.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "forge status: --transcript requires both <execution-id> and <issue-id>")
		return 2
	}
	if !*transcript && fs.NArg() == 2 {
		fmt.Fprintln(os.Stderr, "forge status: <issue-id> is only valid with --transcript")
		return 2
	}

	// A single argument naming a Feature (one with a
	// .forge/features/<id> Planning Artifact directory) is `forge status
	// <feature-id>`, ticket 21's planning-side status view -- distinct from
	// `forge status <execution-id>` below, which reads an Execution's
	// coding-side state. The two ID spaces don't collide in practice
	// (Execution IDs are UUIDs; Feature IDs are operator-chosen slugs), so
	// this is resolved by which directory actually exists on disk rather
	// than by a separate subcommand.
	if fs.NArg() == 1 && !*transcript && isFeatureID(fs.Arg(0)) {
		return runFeatureStatus(fs.Arg(0), *dbPath)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	store, err := openStore(ctx, *dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge status: %v\n", err)
		return 1
	}
	defer func() { _ = store.Close() }()

	// Status is a pure read: it needs only Store, not the tracker,
	// Workspace manager, or Agent buildEngine would otherwise wire up.
	if *transcript {
		events, err := engine.LoadTranscript(ctx, store, fs.Arg(0), fs.Arg(1))
		if err != nil {
			fmt.Fprintf(os.Stderr, "forge status: %v\n", err)
			return 1
		}
		printTranscript(os.Stdout, events)
		return 0
	}

	if fs.NArg() == 0 {
		summaries, err := engine.ListActiveExecutions(ctx, store)
		if err != nil {
			fmt.Fprintf(os.Stderr, "forge status: %v\n", err)
			return 1
		}
		printExecutionSummaries(os.Stdout, summaries)
		return 0
	}
	executionID := fs.Arg(0)

	report, err := engine.LoadStatus(ctx, store, executionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge status: %v\n", err)
		return 1
	}

	printStatus(os.Stdout, report)
	return 0
}

// printTranscript renders one Issue's captured Agent transcript
// (ticket 28), ordered chronologically across every attempt (AgentRun).
func printTranscript(w io.Writer, events []storage.TranscriptEvent) {
	fmt.Fprintf(w, "transcript (%d events):\n", len(events))
	for _, event := range events {
		ts := event.OccurredAt.Format("2006-01-02T15:04:05Z07:00")
		switch event.Type {
		case "TOOL_CALL":
			fmt.Fprintf(w, "  %s  run=%d  tool_call   %s  input=%s\n", ts, event.AgentRunID, event.ToolName, event.ToolInput)
		case "TOOL_RESULT":
			fmt.Fprintf(w, "  %s  run=%d  tool_result %s  output=%s\n", ts, event.AgentRunID, event.ToolName, event.ToolOutput)
		default:
			fmt.Fprintf(w, "  %s  run=%d  %-11s %s: %s\n", ts, event.AgentRunID, strings.ToLower(event.Type), event.Role, event.Text)
		}
	}
}

func printExecutionSummaries(w io.Writer, summaries []engine.ExecutionSummary) {
	fmt.Fprintln(w, "ACTIVE EXECUTIONS")
	fmt.Fprintf(w, "%-36s  %-12s  %-20s  %5s  %5s  %5s  %9s\n",
		"EXECUTION", "BASE", "STARTED", "TOTAL", "LIVE", "DONE", "FAILED")
	for _, summary := range summaries {
		fmt.Fprintf(w, "%-36s  %-12s  %-20s  %5d  %5d  %5d  %9d\n",
			summary.Execution.ID,
			abbrev(summary.Execution.BaseRevision),
			summary.Execution.StartedAt.Format("2006-01-02 15:04:05"),
			summary.IssueCount,
			summary.ActiveIssues,
			summary.DoneIssues,
			summary.FailedIssues,
		)
	}
}

func printStatus(w io.Writer, report engine.StatusReport) {
	fmt.Fprintf(w, "execution %s\n", report.Execution.ID)
	fmt.Fprintf(w, "  base:       %s\n", report.Execution.BaseRevision)
	fmt.Fprintf(w, "  started_at: %s\n", report.Execution.StartedAt.Format("2006-01-02T15:04:05Z07:00"))
	fmt.Fprintf(w, "telemetry:\n")
	fmt.Fprintf(w, "  issues completed:  %d\n", report.Telemetry.Summary.IssuesCompleted)
	fmt.Fprintf(w, "  agent invocations: %d\n", report.Telemetry.Summary.AgentInvocations)
	fmt.Fprintf(w, "  input tokens:      %d\n", report.Telemetry.Summary.InputTokens)
	fmt.Fprintf(w, "  output tokens:     %d\n", report.Telemetry.Summary.OutputTokens)
	fmt.Fprintf(w, "  gate retries:      %d\n", report.Telemetry.Summary.GateRetries)
	fmt.Fprintf(w, "  review retries:    %d\n", report.Telemetry.Summary.ReviewRetries)
	fmt.Fprintf(w, "  ci retries:        %d\n", report.Telemetry.Summary.CIRetries)
	fmt.Fprintf(w, "  context bytes:     %d\n", report.Telemetry.Summary.ContextBytes)
	fmt.Fprintf(w, "  duration:          %s\n", report.Telemetry.Summary.WallClockDuration)

	fmt.Fprintf(w, "issues (%d):\n", len(report.Issues))
	for _, issue := range report.Issues {
		cycle := ""
		for _, metric := range report.Telemetry.Issues {
			if metric.IssueID == issue.Issue.ID && metric.CycleTime > 0 {
				cycle = " cycle=" + metric.CycleTime.String()
				break
			}
		}
		deps := "-"
		if len(issue.Dependencies) > 0 {
			deps = strings.Join(issue.Dependencies, ",")
		}
		worker := issue.WorkerRef
		if worker == "" {
			worker = "-"
		}
		pr := issue.PullRequestURL
		if pr == "" {
			pr = "-"
		}
		failure := issue.Failure
		if failure == "" {
			failure = "-"
		}
		fmt.Fprintf(w, "  %-12s %-18s %-12s %-18s deps=%-12s pr=%s%s\n",
			issue.Issue.ID, issue.Issue.State, issue.Issue.Scope, worker, deps, pr, cycle)
		fmt.Fprintf(w, "    failure=%s\n", failure)
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

func abbrev(sha string) string {
	if len(sha) <= 12 {
		return sha
	}
	return sha[:12]
}
