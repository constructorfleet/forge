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
	if err := store.RecordAgentRun(ctx, run); err != nil {
		t.Fatalf("RecordAgentRun: %v", err)
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
		if err := store.RecordAgentRun(ctx, storage.AgentRun{
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
