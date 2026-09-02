package engine_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/gittest"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/workspace"
)

// newProviderLimitTestEngine builds an Engine whose clock is pinned, so a
// test can assert the exact backoff deadline the Engine schedules.
func newProviderLimitTestEngine(t *testing.T, limits domain.RetryLimits, now time.Time) (*engine.Engine, *storage.SQLiteStore, *agent.FakeAgent, string) {
	t.Helper()
	repoRoot, base := gittest.NewTempRepo(t)
	store := openTestStore(t)
	trk := newFakeTracker()
	trk.issues["7"] = domain.Issue{ID: "7"}
	mgr, err := workspace.NewManager(repoRoot)
	if err != nil {
		t.Fatalf("workspace.NewManager: %v", err)
	}
	fake := agent.NewFakeAgent()
	cfg := config.Default()
	cfg.Retry = limits
	eng := engine.New(store, trk, mgr, fake, cfg, repoRoot)
	eng.NeedsInfoTracker = trk
	eng.Now = func() time.Time { return now }
	return eng, store, fake, base
}

// providerLimitEventData returns the payload of the first issue.provider_limit
// Event recorded for executionID.
func providerLimitEventData(t *testing.T, store *storage.SQLiteStore, executionID, eventType string) map[string]string {
	t.Helper()
	events, err := store.EventsByExecution(context.Background(), executionID)
	if err != nil {
		t.Fatalf("EventsByExecution: %v", err)
	}
	for _, e := range events {
		if e.Type != eventType {
			continue
		}
		var data map[string]string
		if err := json.Unmarshal([]byte(e.Data), &data); err != nil {
			t.Fatalf("unmarshal %s event data: %v", eventType, err)
		}
		return data
	}
	t.Fatalf("no %s event found in %+v", eventType, events)
	return nil
}

// TestExecute_ProviderLimit_ParksAndSchedulesBackoff is the headline test:
// the Agent reports PROVIDER_LIMIT, so the Issue parks in PROVIDER_LIMIT, the
// provider-limit counter advances, and the backoff deadline is persisted.
func TestExecute_ProviderLimit_ParksAndSchedulesBackoff(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	eng, store, fake, base := newProviderLimitTestEngine(t,
		domain.RetryLimits{Gate: 3, Review: 2, CI: 3, ProviderLimit: 3}, now)
	fake.ProgramResult("7", agent.AgentResult{
		Status:  agent.StatusProviderLimit,
		Summary: "provider rate limit reached",
	})

	ctx := context.Background()
	result, err := eng.Execute(ctx, "7", base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateProviderLimit {
		t.Fatalf("final state = %s, want PROVIDER_LIMIT", result.Issue.State)
	}

	issue, err := store.GetIssue(ctx, result.ExecutionID, "7")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.RetryBudget.ProviderLimitFailures() != 1 {
		t.Fatalf("provider-limit stops = %d, want 1", issue.RetryBudget.ProviderLimitFailures())
	}
	if issue.RetryBudget.GateFailures() != 0 || issue.RetryBudget.ReviewFailures() != 0 || issue.RetryBudget.CIFailures() != 0 {
		t.Fatalf("a provider limit must not consume another counter: %+v", issue.RetryBudget)
	}
	wantAt := now.Add(domain.ProviderLimitBackoff(1))
	if issue.ProviderLimitRetryAt == nil || !issue.ProviderLimitRetryAt.Equal(wantAt) {
		t.Fatalf("ProviderLimitRetryAt = %v, want %v", issue.ProviderLimitRetryAt, wantAt)
	}

	data := providerLimitEventData(t, store, result.ExecutionID, "issue.provider_limit")
	if data["attempt"] != "1" {
		t.Errorf("event attempt = %q, want \"1\"", data["attempt"])
	}
	if data["retry_at"] != wantAt.Format(time.RFC3339) {
		t.Errorf("event retry_at = %q, want %q", data["retry_at"], wantAt.Format(time.RFC3339))
	}
}

// TestExecute_ProviderLimit_ExhaustedBudgetRoutesToFailed proves the bounded
// budget ends the wait loop: once the ceiling is reached, the Issue rests in
// FAILED with no new backoff deadline.
func TestExecute_ProviderLimit_ExhaustedBudgetRoutesToFailed(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	eng, store, fake, base := newProviderLimitTestEngine(t,
		domain.RetryLimits{Gate: 3, Review: 2, CI: 3, ProviderLimit: 0}, now)
	fake.ProgramResult("7", agent.AgentResult{
		Status:  agent.StatusProviderLimit,
		Summary: "provider quota exhausted",
	})

	ctx := context.Background()
	result, err := eng.Execute(ctx, "7", base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateFailed {
		t.Fatalf("final state = %s, want FAILED", result.Issue.State)
	}

	issue, err := store.GetIssue(ctx, result.ExecutionID, "7")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.ProviderLimitRetryAt != nil {
		t.Fatalf("ProviderLimitRetryAt = %v, want nil for an exhausted budget", issue.ProviderLimitRetryAt)
	}
	data := providerLimitEventData(t, store, result.ExecutionID, "issue.provider_limit_exhausted")
	if data["limit"] != "0" {
		t.Errorf("event limit = %q, want \"0\"", data["limit"])
	}
}

// TestExecute_ProviderLimit_LastStopFailsWithoutSchedulingAWait proves Forge
// never schedules a backoff it cannot spend: the stop that reaches the
// ceiling routes straight to FAILED.
func TestExecute_ProviderLimit_LastStopFailsWithoutSchedulingAWait(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	eng, store, fake, base := newProviderLimitTestEngine(t,
		domain.RetryLimits{Gate: 3, Review: 2, CI: 3, ProviderLimit: 1}, now)
	fake.ProgramResult("7", agent.AgentResult{
		Status:  agent.StatusProviderLimit,
		Summary: "provider rate limit reached",
	})

	ctx := context.Background()
	result, err := eng.Execute(ctx, "7", base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateFailed {
		t.Fatalf("final state = %s, want FAILED", result.Issue.State)
	}

	issue, err := store.GetIssue(ctx, result.ExecutionID, "7")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.RetryBudget.ProviderLimitFailures() != 1 {
		t.Fatalf("provider-limit stops = %d, want 1", issue.RetryBudget.ProviderLimitFailures())
	}
	if issue.ProviderLimitRetryAt != nil {
		t.Fatalf("ProviderLimitRetryAt = %v, want nil once the ceiling is reached", issue.ProviderLimitRetryAt)
	}
	data := providerLimitEventData(t, store, result.ExecutionID, "issue.provider_limit_exhausted")
	if data["stops"] != "1" || data["limit"] != "1" {
		t.Errorf("event = %+v, want stops=1 limit=1", data)
	}
}
