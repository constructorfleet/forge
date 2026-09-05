package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
)

func newWatchTestStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "forge.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return store
}

func seedCodingExecution(t *testing.T, store *storage.SQLiteStore, id string, state domain.IssueState) {
	t.Helper()
	ctx := context.Background()
	exec := domain.Execution{ID: id, BaseRevision: "abc123", StartedAt: time.Now()}
	if err := store.CreateExecution(ctx, exec); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	issue := domain.Issue{
		ID: id + "-i", ExecutionID: id,
		State: state, Scope: domain.ScopeManaged,
		RetryBudget: domain.NewRetryBudget(domain.RetryLimits{Gate: 3, Review: 3, CI: 3}),
	}
	if err := store.CreateIssue(ctx, issue); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
}

func TestResolveWatchTargetCodingExecution(t *testing.T) {
	store := newWatchTestStore(t)
	seedCodingExecution(t, store, "exec-1", domain.StateImplementing)

	target, err := resolveWatchTarget(context.Background(), store, "exec-1")
	if err != nil {
		t.Fatalf("resolveWatchTarget: %v", err)
	}
	if !target.isCoding || target.id != "exec-1" {
		t.Fatalf("target = %+v, want coding exec-1", target)
	}
}

func TestResolveWatchTargetPlanningExecution(t *testing.T) {
	store := newWatchTestStore(t)
	ctx := context.Background()
	pe := domain.PlanningExecution{ID: "plan-1", FeatureID: "f-1", BaseRevision: "abc", Status: domain.PlanningStatusActive, StartedAt: time.Now()}
	if err := store.CreatePlanningExecution(ctx, pe); err != nil {
		t.Fatalf("CreatePlanningExecution: %v", err)
	}

	target, err := resolveWatchTarget(ctx, store, "plan-1")
	if err != nil {
		t.Fatalf("resolveWatchTarget: %v", err)
	}
	if target.isCoding || target.id != "plan-1" {
		t.Fatalf("target = %+v, want planning plan-1", target)
	}
}

func TestResolveWatchTargetCodingWinsOverPlanning(t *testing.T) {
	store := newWatchTestStore(t)
	ctx := context.Background()
	seedCodingExecution(t, store, "shared", domain.StateImplementing)
	pe := domain.PlanningExecution{ID: "shared", FeatureID: "f-1", BaseRevision: "abc", Status: domain.PlanningStatusActive, StartedAt: time.Now()}
	if err := store.CreatePlanningExecution(ctx, pe); err != nil {
		t.Fatalf("CreatePlanningExecution: %v", err)
	}

	target, err := resolveWatchTarget(ctx, store, "shared")
	if err != nil {
		t.Fatalf("resolveWatchTarget: %v", err)
	}
	if !target.isCoding {
		t.Fatalf("target = %+v, want coding (executions probed first)", target)
	}
}

func TestResolveWatchTargetNotFound(t *testing.T) {
	store := newWatchTestStore(t)
	if _, err := resolveWatchTarget(context.Background(), store, "missing"); err == nil {
		t.Fatal("resolveWatchTarget: want error for unknown id, got nil")
	}
}

func TestListLiveExecutionsFiltersInactive(t *testing.T) {
	store := newWatchTestStore(t)
	ctx := context.Background()
	seedCodingExecution(t, store, "exec-active", domain.StateImplementing)
	seedCodingExecution(t, store, "exec-done", domain.StateDone)

	live, err := listLiveExecutions(ctx, store)
	if err != nil {
		t.Fatalf("listLiveExecutions: %v", err)
	}
	if len(live) != 1 {
		t.Fatalf("live executions = %d, want 1", len(live))
	}
	if live[0].ID != "exec-active" {
		t.Fatalf("live[0].ID = %q, want exec-active", live[0].ID)
	}
	if live[0].Active != 1 {
		t.Fatalf("live[0].Active = %d, want 1", live[0].Active)
	}
}

func TestRunWatch_DefaultDBResolvesFromRepoRoot(t *testing.T) {
	repoRoot, _ := newTempRepo(t)
	if err := os.MkdirAll(filepath.Join(repoRoot, ".forge"), 0o755); err != nil {
		t.Fatalf("MkdirAll .forge: %v", err)
	}
	store, err := storage.Open(filepath.Join(repoRoot, defaultDBPath))
	if err != nil {
		t.Fatalf("Open root store: %v", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate root store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close root store: %v", err)
	}

	subdir := filepath.Join(repoRoot, "sub", "dir")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("MkdirAll subdir: %v", err)
	}
	chdir(t, subdir)

	stderr := captureStderr(t, func() {
		if code := runWatch(nil); code != 1 {
			t.Fatalf("runWatch = %d, want 1", code)
		}
	})
	if !strings.Contains(stderr, "forge watch: no active executions") {
		t.Fatalf("stderr = %q, want root store no-active-executions message", stderr)
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	original := os.Stderr
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	os.Stderr = write
	t.Cleanup(func() { os.Stderr = original })

	fn()

	if err := write.Close(); err != nil {
		t.Fatalf("Close pipe writer: %v", err)
	}
	data, err := io.ReadAll(read)
	if err != nil {
		t.Fatalf("ReadAll stderr: %v", err)
	}
	if err := read.Close(); err != nil {
		t.Fatalf("Close pipe reader: %v", err)
	}
	os.Stderr = original
	return string(data)
}
