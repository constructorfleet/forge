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

// chdir switches the process's working directory to dir for the duration of
// the test, restoring the original on cleanup.
func chdir(t *testing.T, dir string) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
}

func TestDiscoverRepoRoot_ResolvesTopLevelFromSubdirectory(t *testing.T) {
	repoRoot, _ := newTempRepo(t)
	sub := filepath.Join(repoRoot, "sub", "dir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	chdir(t, sub)

	got, err := discoverRepoRoot()
	if err != nil {
		t.Fatalf("discoverRepoRoot: %v", err)
	}
	// macOS temp dirs resolve through a symlink (/tmp -> /private/tmp), so
	// compare resolved paths rather than raw strings.
	wantResolved, err := filepath.EvalSymlinks(repoRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks(want): %v", err)
	}
	gotResolved, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatalf("EvalSymlinks(got): %v", err)
	}
	if gotResolved != wantResolved {
		t.Fatalf("discoverRepoRoot = %q, want %q", gotResolved, wantResolved)
	}
}

func TestDiscoverRepoRoot_FailsOutsideGitRepo(t *testing.T) {
	chdir(t, t.TempDir())

	if _, err := discoverRepoRoot(); err == nil {
		t.Fatal("discoverRepoRoot: want error outside a git repository, got nil")
	}
}

// TestRunRetry_ResolvesPathsFromSubdirectory covers issue #459: running
// `forge retry` from a subdirectory must use the repo root's .forge.yaml
// and .forge/forge.db, not create fresh, empty ones under the subdirectory.
func TestRunRetry_ResolvesPathsFromSubdirectory(t *testing.T) {
	repoRoot, base := newTempRepo(t)
	runGit(t, repoRoot, "remote", "add", "origin", "git@github.com:acme/widgets.git")

	cfgPath := filepath.Join(repoRoot, ".forge.yaml")
	if err := os.WriteFile(cfgPath, []byte("version: 1\nagent:\n  provider: fake\ngit:\n  base: main\ntracker:\n  skip_auth_preflight: true\npull_requests:\n  enabled: false\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	dbPath := filepath.Join(repoRoot, ".forge", "forge.db")
	store, err := openStore(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	exec := domain.Execution{ID: "exec-retry-cli", BaseRevision: base, StartedAt: time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)}
	if err := store.CreateExecution(context.Background(), exec); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	if err := store.CreateIssue(context.Background(), domain.Issue{
		ID:          "88",
		ExecutionID: exec.ID,
		Title:       "Retry from CLI",
		State:       domain.StateFailed,
		Scope:       domain.ScopeManaged,
		RetryBudget: domain.NewRetryBudget(config.Default().Retry),
	}); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.AppendEvent(context.Background(), storage.Event{
		ExecutionID: exec.ID,
		IssueID:     "88",
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

	if code := runRetry([]string{exec.ID + "/88"}); code != 0 {
		t.Fatalf("runRetry = %d, want 0", code)
	}

	if _, err := os.Stat(filepath.Join(sub, ".forge")); !os.IsNotExist(err) {
		t.Fatalf("expected no .forge directory under %s, stat err = %v", sub, err)
	}

	reopened, err := openStore(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer func() { _ = reopened.Close() }()
	issue, err := reopened.GetIssue(context.Background(), exec.ID, "88")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.State != domain.StateCommitting {
		t.Fatalf("persisted state = %s, want COMMITTING", issue.State)
	}
}

func TestRunRetry_FailsLoudlyOutsideGitRepo(t *testing.T) {
	chdir(t, t.TempDir())

	if code := runRetry([]string{"exec-1/1"}); code == 0 {
		t.Fatal("runRetry = 0, want non-zero outside a git repository")
	}
}
