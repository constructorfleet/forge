package storage_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
)

func seedIssueForGateRun(t *testing.T, store *storage.SQLiteStore, executionID, issueID string) {
	t.Helper()
	ctx := context.Background()
	if err := store.CreateExecution(ctx, domain.Execution{ID: executionID, BaseRevision: "abc123", StartedAt: time.Now()}); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	issue := domain.Issue{
		ID: issueID, ExecutionID: executionID,
		State: domain.StatePending, Scope: domain.ScopeManaged,
		RetryBudget: domain.NewRetryBudget(domain.RetryLimits{Gate: 3, Review: 3, CI: 3}),
	}
	if err := store.CreateIssue(ctx, issue); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
}

func TestRecordGateRun_PersistsAndAppendsEvent(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedIssueForGateRun(t, store, "exec-gate", "issue-gate")

	started := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	finished := started.Add(2 * time.Second)
	run := storage.GateRun{
		ExecutionID: "exec-gate",
		IssueID:     "issue-gate",
		Name:        "lint",
		Command:     "make lint",
		StartedAt:   started,
		FinishedAt:  finished,
		ExitCode:    1,
		Stdout:      "some output",
		Stderr:      "some error",
		Passed:      false,
	}
	if err := store.RecordGateRun(ctx, run); err != nil {
		t.Fatalf("RecordGateRun: %v", err)
	}

	runs, err := store.GateRunsByIssue(ctx, "exec-gate", "issue-gate")
	if err != nil {
		t.Fatalf("GateRunsByIssue: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d gate runs, want 1", len(runs))
	}
	got := runs[0]
	if got.Name != "lint" || got.Command != "make lint" {
		t.Errorf("Name/Command = %s/%s", got.Name, got.Command)
	}
	if got.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", got.ExitCode)
	}
	if got.Stdout != "some output" || got.Stderr != "some error" {
		t.Errorf("Stdout/Stderr = %q/%q", got.Stdout, got.Stderr)
	}
	if got.Passed {
		t.Error("Passed = true, want false")
	}
	if !got.StartedAt.Equal(started) {
		t.Errorf("StartedAt = %v, want %v", got.StartedAt, started)
	}
	if !got.FinishedAt.Equal(finished) {
		t.Errorf("FinishedAt = %v, want %v", got.FinishedAt, finished)
	}

	events, err := store.EventsByIssue(ctx, "exec-gate", "issue-gate")
	if err != nil {
		t.Fatalf("EventsByIssue: %v", err)
	}
	if len(events) != 1 || events[0].Type != "gate.run" {
		t.Fatalf("events = %+v, want one gate.run event", events)
	}
	var payload struct {
		Name     string `json:"name"`
		Command  string `json:"command"`
		ExitCode int    `json:"exit_code"`
		Passed   bool   `json:"passed"`
	}
	if err := json.Unmarshal([]byte(events[0].Data), &payload); err != nil {
		t.Fatalf("unmarshal gate.run event: %v", err)
	}
	if payload.Name != "lint" || payload.Command != "make lint" || payload.ExitCode != 1 || payload.Passed {
		t.Errorf("gate.run event payload = %+v", payload)
	}
}

func TestRecordGateRun_MultipleRunsOrderedByInsertion(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedIssueForGateRun(t, store, "exec-gate-2", "issue-gate-2")

	for _, name := range []string{"test", "lint", "build"} {
		run := storage.GateRun{
			ExecutionID: "exec-gate-2",
			IssueID:     "issue-gate-2",
			Name:        name,
			Command:     "make " + name,
			StartedAt:   time.Now(),
			FinishedAt:  time.Now(),
			ExitCode:    0,
			Passed:      true,
		}
		if err := store.RecordGateRun(ctx, run); err != nil {
			t.Fatalf("RecordGateRun(%s): %v", name, err)
		}
	}

	runs, err := store.GateRunsByIssue(ctx, "exec-gate-2", "issue-gate-2")
	if err != nil {
		t.Fatalf("GateRunsByIssue: %v", err)
	}
	if len(runs) != 3 {
		t.Fatalf("got %d gate runs, want 3", len(runs))
	}
	wantOrder := []string{"test", "lint", "build"}
	for i, want := range wantOrder {
		if runs[i].Name != want {
			t.Errorf("runs[%d].Name = %s, want %s", i, runs[i].Name, want)
		}
	}
}
