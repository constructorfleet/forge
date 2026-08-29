package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
)

func TestRunResume_ReconcilesReadyExecution(t *testing.T) {
	repoRoot, base := newTempRepo(t)
	runGit(t, repoRoot, "remote", "add", "origin", "git@github.com:acme/widgets.git")

	cfgPath := filepath.Join(repoRoot, ".forge.yaml")
	// pull_requests.enabled: false leaves the commit/PR seam (Publisher/
	// PRTracker) unwired, so this hermetic test drives the full state
	// machine to its COMMITTING resting state without a live GitHub remote.
	if err := os.WriteFile(cfgPath, []byte("version: 1\nagent:\n  provider: fake\ntracker:\n  skip_auth_preflight: true\npull_requests:\n  enabled: false\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	dbPath := filepath.Join(repoRoot, ".forge", "forge.db")
	store, err := openStore(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	exec := domain.Execution{ID: "exec-resume-cli", BaseRevision: base, StartedAt: time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)}
	if err := store.CreateExecution(context.Background(), exec); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	if err := store.CreateIssue(context.Background(), domain.Issue{
		ID:          "77",
		ExecutionID: exec.ID,
		Title:       "Resume from CLI",
		State:       domain.StateReady,
		Scope:       domain.ScopeManaged,
		RetryBudget: domain.NewRetryBudget(config.Default().Retry),
	}); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.AppendEvent(context.Background(), storage.Event{
		ExecutionID: exec.ID,
		IssueID:     "77",
		Type:        "worker.base_captured",
		Data:        `{"base":"` + base + `"}`,
		OccurredAt:  time.Date(2026, 8, 28, 12, 1, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if err := os.Chdir(repoRoot); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	if code := runResume([]string{"--config", cfgPath, "--db", dbPath, exec.ID}); code != 0 {
		t.Fatalf("runResume = %d, want 0", code)
	}

	reopened, err := openStore(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer func() { _ = reopened.Close() }()
	issue, err := reopened.GetIssue(context.Background(), exec.ID, "77")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.State != domain.StateCommitting {
		t.Fatalf("persisted state = %s, want COMMITTING", issue.State)
	}
}
