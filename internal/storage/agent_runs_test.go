package storage_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
)

func seedIssueForAgentRun(t *testing.T, store *storage.SQLiteStore, executionID, issueID string) {
	t.Helper()
	ctx := context.Background()
	if err := store.CreateExecution(ctx, domain.Execution{ID: executionID, BaseRevision: "abc123", StartedAt: time.Now()}); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	issue := domain.Issue{
		ID: issueID, ExecutionID: executionID,
		State: domain.StateImplementing, Scope: domain.ScopeManaged,
		RetryBudget: domain.NewRetryBudget(domain.RetryLimits{Gate: 3, Review: 3, CI: 3}),
	}
	if err := store.CreateIssue(ctx, issue); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
}

func TestRecordAgentRun_PersistsAndAppendsEvent(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedIssueForAgentRun(t, store, "exec-agent", "issue-agent")

	inputTokens := 123
	outputTokens := 45
	run := storage.AgentRun{
		ExecutionID:  "exec-agent",
		IssueID:      "issue-agent",
		Backend:      "claude-code",
		StartedAt:    time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC),
		FinishedAt:   time.Date(2026, 8, 28, 12, 0, 3, 0, time.UTC),
		Result:       "IMPLEMENTED",
		ContextBytes: 2048,
		InputTokens:  &inputTokens,
		OutputTokens: &outputTokens,
	}
	runID, err := store.RecordAgentRun(ctx, run)
	if err != nil {
		t.Fatalf("RecordAgentRun: %v", err)
	}
	if runID <= 0 {
		t.Fatalf("RecordAgentRun returned id = %d, want positive", runID)
	}

	runs, err := store.AgentRunsByIssue(ctx, "exec-agent", "issue-agent")
	if err != nil {
		t.Fatalf("AgentRunsByIssue: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d agent runs, want 1", len(runs))
	}
	got := runs[0]
	if got.Backend != run.Backend || got.Result != run.Result || got.ContextBytes != run.ContextBytes {
		t.Fatalf("persisted run = %+v, want backend/result/context copied", got)
	}
	if got.InputTokens == nil || *got.InputTokens != inputTokens {
		t.Fatalf("InputTokens = %+v, want %d", got.InputTokens, inputTokens)
	}
	if got.OutputTokens == nil || *got.OutputTokens != outputTokens {
		t.Fatalf("OutputTokens = %+v, want %d", got.OutputTokens, outputTokens)
	}

	events, err := store.EventsByIssue(ctx, "exec-agent", "issue-agent")
	if err != nil {
		t.Fatalf("EventsByIssue: %v", err)
	}
	if len(events) != 1 || events[0].Type != "agent.run" {
		t.Fatalf("events = %+v, want one agent.run event", events)
	}
	var payload struct {
		Backend      string `json:"backend"`
		Result       string `json:"result"`
		ContextBytes int    `json:"context_bytes"`
		InputTokens  *int   `json:"input_tokens"`
		OutputTokens *int   `json:"output_tokens"`
	}
	if err := json.Unmarshal([]byte(events[0].Data), &payload); err != nil {
		t.Fatalf("unmarshal agent.run event: %v", err)
	}
	if payload.Backend != "claude-code" || payload.Result != "IMPLEMENTED" || payload.ContextBytes != 2048 {
		t.Fatalf("payload = %+v", payload)
	}
	if payload.InputTokens == nil || *payload.InputTokens != inputTokens {
		t.Fatalf("payload.InputTokens = %+v, want %d", payload.InputTokens, inputTokens)
	}
	if payload.OutputTokens == nil || *payload.OutputTokens != outputTokens {
		t.Fatalf("payload.OutputTokens = %+v, want %d", payload.OutputTokens, outputTokens)
	}
}

func TestAgentRunsByExecution_ReturnsRunsAcrossIssuesInInsertionOrder(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	if err := store.CreateExecution(ctx, domain.Execution{ID: "exec-agent-2", BaseRevision: "abc123", StartedAt: time.Now()}); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	for _, issueID := range []string{"issue-a", "issue-b"} {
		issue := domain.Issue{
			ID: issueID, ExecutionID: "exec-agent-2",
			State: domain.StateImplementing, Scope: domain.ScopeManaged,
			RetryBudget: domain.NewRetryBudget(domain.RetryLimits{Gate: 3, Review: 3, CI: 3}),
		}
		if err := store.CreateIssue(ctx, issue); err != nil {
			t.Fatalf("CreateIssue(%s): %v", issueID, err)
		}
	}

	for _, issueID := range []string{"issue-a", "issue-b"} {
		if _, err := store.RecordAgentRun(ctx, storage.AgentRun{
			ExecutionID:  "exec-agent-2",
			IssueID:      issueID,
			Backend:      "claude-code",
			StartedAt:    time.Now(),
			FinishedAt:   time.Now(),
			Result:       "IMPLEMENTED",
			ContextBytes: 1,
		}); err != nil {
			t.Fatalf("RecordAgentRun(%s): %v", issueID, err)
		}
	}

	runs, err := store.AgentRunsByExecution(ctx, "exec-agent-2")
	if err != nil {
		t.Fatalf("AgentRunsByExecution: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("got %d runs, want 2", len(runs))
	}
	if runs[0].IssueID != "issue-a" || runs[1].IssueID != "issue-b" {
		t.Fatalf("runs = %+v, want insertion order [issue-a issue-b]", runs)
	}
}

func TestStartAndFinalizeAgentRun_CreatesRunningThenUpdatesOnFinalize(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedIssueForAgentRun(t, store, "exec-start-final", "issue-start-final")

	startedAt := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	runID, err := store.StartAgentRun(ctx, storage.AgentRun{
		ExecutionID:  "exec-start-final",
		IssueID:      "issue-start-final",
		Backend:      "claude-code",
		StartedAt:    startedAt,
		ContextBytes: 1024,
	})
	if err != nil {
		t.Fatalf("StartAgentRun: %v", err)
	}
	if runID <= 0 {
		t.Fatalf("StartAgentRun returned id = %d, want positive", runID)
	}

	runs, err := store.AgentRunsByIssue(ctx, "exec-start-final", "issue-start-final")
	if err != nil {
		t.Fatalf("AgentRunsByIssue after StartAgentRun: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs after StartAgentRun, want 1", len(runs))
	}
	got := runs[0]
	if got.Backend != "claude-code" {
		t.Fatalf("backend = %q, want claude-code", got.Backend)
	}
	if got.Result != storage.AgentRunResultRunning {
		t.Fatalf("result = %q, want RUNNING", got.Result)
	}
	if !got.StartedAt.Equal(startedAt) {
		t.Fatalf("StartedAt = %v, want %v", got.StartedAt, startedAt)
	}
	if !got.FinishedAt.Equal(startedAt) {
		t.Fatalf("FinishedAt = %v, want placeholder = StartedAt", got.FinishedAt)
	}
	if got.ContextBytes != 1024 {
		t.Fatalf("ContextBytes = %d, want 1024", got.ContextBytes)
	}

	events, err := store.EventsByIssue(ctx, "exec-start-final", "issue-start-final")
	if err != nil {
		t.Fatalf("EventsByIssue after StartAgentRun: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %d, want 0 (no agent.run event yet)", len(events))
	}

	finishedAt := time.Date(2026, 8, 28, 12, 0, 5, 0, time.UTC)
	inputTokens := 100
	outputTokens := 50
	if err := store.FinalizeAgentRun(ctx, runID, storage.AgentRun{
		ExecutionID:  "exec-start-final",
		IssueID:      "issue-start-final",
		Backend:      "claude-code",
		StartedAt:    startedAt,
		FinishedAt:   finishedAt,
		Result:       "IMPLEMENTED",
		ContextBytes: 1024,
		InputTokens:  &inputTokens,
		OutputTokens: &outputTokens,
	}); err != nil {
		t.Fatalf("FinalizeAgentRun: %v", err)
	}

	runs, err = store.AgentRunsByIssue(ctx, "exec-start-final", "issue-start-final")
	if err != nil {
		t.Fatalf("AgentRunsByIssue after FinalizeAgentRun: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs after FinalizeAgentRun, want 1", len(runs))
	}
	got = runs[0]
	if got.Result != "IMPLEMENTED" {
		t.Fatalf("result = %q, want IMPLEMENTED", got.Result)
	}
	if !got.FinishedAt.Equal(finishedAt) {
		t.Fatalf("FinishedAt = %v, want %v", got.FinishedAt, finishedAt)
	}
	if got.InputTokens == nil || *got.InputTokens != inputTokens {
		t.Fatalf("InputTokens = %+v, want %d", got.InputTokens, inputTokens)
	}
	if got.OutputTokens == nil || *got.OutputTokens != outputTokens {
		t.Fatalf("OutputTokens = %+v, want %d", got.OutputTokens, outputTokens)
	}

	events, err = store.EventsByIssue(ctx, "exec-start-final", "issue-start-final")
	if err != nil {
		t.Fatalf("EventsByIssue after FinalizeAgentRun: %v", err)
	}
	if len(events) != 1 || events[0].Type != "agent.run" {
		t.Fatalf("events = %+v, want one agent.run event", events)
	}
	var payload struct {
		Backend      string `json:"backend"`
		Result       string `json:"result"`
		ContextBytes int    `json:"context_bytes"`
		InputTokens  *int   `json:"input_tokens"`
		OutputTokens *int   `json:"output_tokens"`
	}
	if err := json.Unmarshal([]byte(events[0].Data), &payload); err != nil {
		t.Fatalf("unmarshal agent.run event: %v", err)
	}
	if payload.Backend != "claude-code" || payload.Result != "IMPLEMENTED" || payload.ContextBytes != 1024 {
		t.Fatalf("payload = %+v", payload)
	}
	if payload.InputTokens == nil || *payload.InputTokens != inputTokens {
		t.Fatalf("payload.InputTokens = %+v, want %d", payload.InputTokens, inputTokens)
	}
	if payload.OutputTokens == nil || *payload.OutputTokens != outputTokens {
		t.Fatalf("payload.OutputTokens = %+v, want %d", payload.OutputTokens, outputTokens)
	}
}

func TestStartAgentRunWithoutFinalize_LeavesRunningResult(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedIssueForAgentRun(t, store, "exec-interrupted", "issue-interrupted")

	_, err := store.StartAgentRun(ctx, storage.AgentRun{
		ExecutionID:  "exec-interrupted",
		IssueID:      "issue-interrupted",
		Backend:      "claude-code",
		StartedAt:    time.Now(),
		ContextBytes: 512,
	})
	if err != nil {
		t.Fatalf("StartAgentRun: %v", err)
	}

	runs, err := store.AgentRunsByIssue(ctx, "exec-interrupted", "issue-interrupted")
	if err != nil {
		t.Fatalf("AgentRunsByIssue: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1", len(runs))
	}
	if runs[0].Result != storage.AgentRunResultRunning {
		t.Fatalf("result = %q, want RUNNING (interrupted run)", runs[0].Result)
	}

	events, err := store.EventsByIssue(ctx, "exec-interrupted", "issue-interrupted")
	if err != nil {
		t.Fatalf("EventsByIssue: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %d, want 0 (interrupted run never finalized)", len(events))
	}
}
