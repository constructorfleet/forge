package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/storage"
)

// TestRunExecute_RejectsNoIssueIDs asserts `forge execute` requires at
// least one Issue ID; ticket 26 lifted the single-argument restriction so
// `forge execute 345 344 343` is now valid (buildScheduler/scheduler.Run's
// multi-issue behavior itself is covered end-to-end in
// internal/scheduler's tests, which don't require a real "origin" remote
// the way runExecute's buildTracker does).
func TestRunExecute_RejectsNoIssueIDs(t *testing.T) {
	if code := runExecute(nil); code != 2 {
		t.Errorf("runExecute(nil) = %d, want 2 (usage error)", code)
	}
}

func TestRunStatus_RejectsWrongArgCount(t *testing.T) {
	for _, args := range [][]string{{"a", "b"}} {
		if code := runStatus(args); code != 2 {
			t.Errorf("runStatus(%v) = %d, want 2 (usage error)", args, code)
		}
	}
}

func TestPrintExecutionSummaries_ListsOnlyActiveExecutions(t *testing.T) {
	summaries := []engine.ExecutionSummary{
		{
			Execution:       domain.Execution{ID: "exec-active", BaseRevision: "deadbeef", StartedAt: time.Unix(0, 0).UTC()},
			ActiveIssues:    2,
			FailedIssues:    1,
			DoneIssues:      0,
			CancelledIssues: 0,
		},
	}

	var buf bytes.Buffer
	printExecutionSummaries(&buf, summaries)
	out := buf.String()

	for _, want := range []string{"ACTIVE EXECUTIONS", "exec-active", "deadbeef", "2", "1"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary output missing %q:\n%s", want, out)
		}
	}
}

func TestRunStatus_ReturnsErrorForUnknownExecution(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "forge.db")
	code := runStatus([]string{"--db", dbPath, "does-not-exist"})
	if code != 1 {
		t.Errorf("runStatus for unknown execution = %d, want 1", code)
	}
}

func TestRunStatus_WithoutExecutionID_ListsActiveExecutions(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "forge.db")
	ctx := context.Background()
	store, err := openStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	if err := store.CreateExecution(ctx, domain.Execution{
		ID:           "exec-active",
		BaseRevision: "deadbeefcafebabe",
		StartedAt:    time.Unix(0, 0).UTC(),
	}); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	if err := store.CreateIssue(ctx, domain.Issue{
		ID:          "7",
		ExecutionID: "exec-active",
		State:       domain.StateImplementing,
		Scope:       domain.ScopeManaged,
		RetryBudget: domain.NewRetryBudget(config.Default().Retry),
	}); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	if code := runStatus([]string{"--db", dbPath}); code != 0 {
		t.Fatalf("runStatus(list) = %d, want 0", code)
	}
}

func TestPrintStatus_IncludesExecutionIssuesAndEvents(t *testing.T) {
	report := engine.StatusReport{
		Execution: domain.Execution{ID: "exec-1", BaseRevision: "deadbeef", StartedAt: time.Unix(0, 0).UTC()},
		Issues: []engine.IssueStatus{
			{
				Issue:          domain.Issue{ID: "42", State: domain.StateValidating, Scope: domain.ScopeManaged},
				WorkerRef:      "worker-exec-1-42",
				PullRequestURL: "https://example.invalid/pr/42",
				Failure:        "gate test failed",
				Dependencies:   []string{"41"},
			},
		},
		Telemetry: engine.TelemetryReport{
			Summary: engine.TelemetrySummary{
				IssuesCompleted:   1,
				AgentInvocations:  2,
				InputTokens:       123,
				OutputTokens:      45,
				GateRetries:       1,
				ReviewRetries:     2,
				CIRetries:         3,
				ContextBytes:      2048,
				WallClockDuration: 5 * time.Second,
			},
		},
		Events: []storage.Event{
			{IssueID: "42", Type: "issue.transitioned", Data: `{"from":"IMPLEMENTING","to":"VALIDATING"}`, OccurredAt: time.Unix(0, 0).UTC()},
		},
	}

	var buf bytes.Buffer
	printStatus(&buf, report)
	out := buf.String()

	for _, want := range []string{
		"exec-1", "deadbeef", "42", string(domain.StateValidating), "worker-exec-1-42",
		"https://example.invalid/pr/42", "gate test failed", "41", "issue.transitioned", "agent invocations", "input tokens",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("printStatus output missing %q:\n%s", want, out)
		}
	}
}

// TestRunStatus_ReflectsExecuteOutput drives Engine directly (as
// buildEngine would, minus the GitHub/git specifics runExecute's own flags
// resolve) to populate a real SQLite database, then exercises runStatus
// against it end-to-end.
func TestRunStatus_ReflectsExecuteOutput(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "forge.db")
	ctx := context.Background()
	store, err := openStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}

	root, base := newTempRepo(t)
	wsMgr := mustWorkspaceManager(t, root)
	trk := &stubTrackerForCLI{issue: domain.Issue{ID: "5"}}
	fake := newProgrammedFakeAgent(t, "5")

	eng := engine.New(store, trk, wsMgr, fake, mustConfig(), root)
	result, err := eng.Execute(ctx, "5", base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	code := runStatus([]string{"--db", dbPath, result.ExecutionID})
	if code != 0 {
		t.Fatalf("runStatus = %d, want 0", code)
	}
}
