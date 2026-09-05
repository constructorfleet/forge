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

// TestRunResume_FailsLoudlyOutsideGitRepo covers issue #576: `forge resume`
// must discover the repo root via git (as `forge retry` already does for
// issue #459) instead of silently treating an arbitrary cwd as the repo
// root.
func TestRunResume_FailsLoudlyOutsideGitRepo(t *testing.T) {
	chdir(t, t.TempDir())

	if code := runResume([]string{"exec-1"}); code != 1 {
		t.Fatalf("runResume outside a git repo = %d, want 1", code)
	}
}

// TestRunResume_ResolvesPathsFromSubdirectory covers issue #576: running
// `forge resume` from a subdirectory, with --config/--db left at their
// defaults, must use the repo root's .forge.yaml and .forge/forge.db, not
// create fresh, empty ones under the subdirectory (the same #459 bug class
// `forge retry` was already fixed for).
func TestRunResume_ResolvesPathsFromSubdirectory(t *testing.T) {
	repoRoot, base := newTempRepo(t)
	runGit(t, repoRoot, "remote", "add", "origin", "git@github.com:acme/widgets.git")

	cfgPath := filepath.Join(repoRoot, ".forge.yaml")
	if err := os.WriteFile(cfgPath, []byte("version: 1\nagent:\n  provider: fake\ntracker:\n  skip_auth_preflight: true\npull_requests:\n  enabled: false\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	dbPath := filepath.Join(repoRoot, ".forge", "forge.db")
	store, err := openStore(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	exec := domain.Execution{ID: "exec-resume-subdir", BaseRevision: base, StartedAt: time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)}
	if err := store.CreateExecution(context.Background(), exec); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	if err := store.CreateIssue(context.Background(), domain.Issue{
		ID:          "78",
		ExecutionID: exec.ID,
		Title:       "Resume from subdirectory",
		State:       domain.StateReady,
		Scope:       domain.ScopeManaged,
		RetryBudget: domain.NewRetryBudget(config.Default().Retry),
	}); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.AppendEvent(context.Background(), storage.Event{
		ExecutionID: exec.ID,
		IssueID:     "78",
		Type:        "worker.base_captured",
		Data:        `{"base":"` + base + `"}`,
		OccurredAt:  time.Date(2026, 8, 28, 12, 1, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	sub := filepath.Join(repoRoot, "sub", "dir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	chdir(t, sub)

	if code := runResume([]string{exec.ID}); code != 0 {
		t.Fatalf("runResume = %d, want 0", code)
	}

	if _, err := os.Stat(filepath.Join(sub, ".forge")); !os.IsNotExist(err) {
		t.Fatalf("expected no .forge directory under %s, stat err = %v", sub, err)
	}

	reopened, err := openStore(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer func() { _ = reopened.Close() }()
	issue, err := reopened.GetIssue(context.Background(), exec.ID, "78")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.State != domain.StateCommitting {
		t.Fatalf("persisted state = %s, want COMMITTING", issue.State)
	}
}
