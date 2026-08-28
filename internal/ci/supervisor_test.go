package ci_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/ci"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
)

func openTestStore(t *testing.T) *storage.SQLiteStore {
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

type stubTracker struct {
	mergeRequirements tracker.MergeRequirements
	mergeErr          error
	checkResponses    [][]tracker.PullRequestCheck
	checkErr          error

	mergeCalls int
	checkCalls int
}

func (s *stubTracker) GetMergeRequirements(context.Context, string) (tracker.MergeRequirements, error) {
	s.mergeCalls++
	return s.mergeRequirements, s.mergeErr
}

func (s *stubTracker) GetPullRequestChecks(context.Context, int) ([]tracker.PullRequestCheck, error) {
	s.checkCalls++
	if s.checkErr != nil {
		return nil, s.checkErr
	}
	if len(s.checkResponses) == 0 {
		return nil, nil
	}
	idx := s.checkCalls - 1
	if idx >= len(s.checkResponses) {
		idx = len(s.checkResponses) - 1
	}
	return append([]tracker.PullRequestCheck(nil), s.checkResponses[idx]...), nil
}

type sleepRecorder struct {
	calls int
}

func (s *sleepRecorder) Sleep(context.Context, time.Duration) error {
	s.calls++
	return nil
}

func seedIssueWithPR(t *testing.T, store *storage.SQLiteStore, executionID, issueID string) {
	t.Helper()
	ctx := context.Background()
	if err := store.CreateExecution(ctx, domain.Execution{
		ID:           executionID,
		BaseRevision: "base",
		StartedAt:    time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	issue := domain.Issue{
		ID:          issueID,
		ExecutionID: executionID,
		Title:       "Issue " + issueID,
		State:       domain.StateCIPending,
		Scope:       domain.ScopeManaged,
		RetryBudget: domain.NewRetryBudget(domain.RetryLimits{Gate: 1, Review: 1, CI: 3}),
	}
	if err := store.CreateIssue(ctx, issue); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.RecordPullRequest(ctx, storage.PullRequest{
		ExecutionID: executionID,
		IssueID:     issueID,
		Number:      23,
		URL:         "https://example.invalid/pr/23",
		CommitSHA:   "abc123",
		CreatedAt:   time.Date(2026, 8, 28, 12, 1, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("RecordPullRequest: %v", err)
	}
}

func TestWait_AllRequiredChecksGreen_TransitionsToDone(t *testing.T) {
	store := openTestStore(t)
	seedIssueWithPR(t, store, "exec-1", "23")

	trk := &stubTracker{
		mergeRequirements: tracker.MergeRequirements{RequiredChecks: []string{"build", "test"}},
		checkResponses: [][]tracker.PullRequestCheck{
			{
				{Name: "build", State: tracker.CheckPending},
				{Name: "test", State: tracker.CheckPending},
			},
			{
				{Name: "build", State: tracker.CheckSuccess},
				{Name: "test", State: tracker.CheckSuccess},
				{Name: "optional", State: tracker.CheckFailure, Details: "non-blocking"},
			},
		},
	}
	sleep := &sleepRecorder{}

	supervisor := ci.New(store, trk, config.Default(), "main")
	supervisor.Sleep = sleep.Sleep
	supervisor.Now = func() time.Time { return time.Date(2026, 8, 28, 12, 2, sleep.calls, 0, time.UTC) }

	state, err := supervisor.Wait(context.Background(), "exec-1", "23")
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if state != domain.StateDone {
		t.Fatalf("state = %s, want DONE", state)
	}

	issue, err := store.GetIssue(context.Background(), "exec-1", "23")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.State != domain.StateDone {
		t.Fatalf("persisted state = %s, want DONE", issue.State)
	}

	runs, err := store.CIRunsByIssue(context.Background(), "exec-1", "23")
	if err != nil {
		t.Fatalf("CIRunsByIssue: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("got %d CI runs, want 2", len(runs))
	}
	if runs[0].Status != storage.CIRunStatusPending || runs[1].Status != storage.CIRunStatusPassed {
		t.Fatalf("run statuses = [%s %s], want [PENDING PASSED]", runs[0].Status, runs[1].Status)
	}
	if got := runs[1].CheckName; got != "" {
		t.Fatalf("passed run CheckName = %q, want empty", got)
	}
	if trk.mergeCalls != 1 {
		t.Fatalf("GetMergeRequirements calls = %d, want 1", trk.mergeCalls)
	}
	if trk.checkCalls != 2 {
		t.Fatalf("GetPullRequestChecks calls = %d, want 2", trk.checkCalls)
	}
	if sleep.calls != 1 {
		t.Fatalf("sleep calls = %d, want 1", sleep.calls)
	}
}

func TestWait_RequiredCheckFailure_TransitionsToCIFailedWithBoundedDiagnostics(t *testing.T) {
	store := openTestStore(t)
	seedIssueWithPR(t, store, "exec-2", "24")

	trk := &stubTracker{
		mergeRequirements: tracker.MergeRequirements{RequiredChecks: []string{"build"}},
		checkResponses: [][]tracker.PullRequestCheck{
			{
				{Name: "build", State: tracker.CheckFailure, Details: "0123456789"},
				{Name: "optional", State: tracker.CheckSuccess},
			},
		},
	}

	cfg := config.Default()
	cfg.CI.MaxOutputBytes = 4
	supervisor := ci.New(store, trk, cfg, "main")
	supervisor.Now = func() time.Time { return time.Date(2026, 8, 28, 12, 3, 0, 0, time.UTC) }

	state, err := supervisor.Wait(context.Background(), "exec-2", "24")
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if state != domain.StateCIFailed {
		t.Fatalf("state = %s, want CI_FAILED", state)
	}

	issue, err := store.GetIssue(context.Background(), "exec-2", "24")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.State != domain.StateCIFailed {
		t.Fatalf("persisted state = %s, want CI_FAILED", issue.State)
	}

	runs, err := store.CIRunsByIssue(context.Background(), "exec-2", "24")
	if err != nil {
		t.Fatalf("CIRunsByIssue: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d CI runs, want 1", len(runs))
	}
	if runs[0].Status != storage.CIRunStatusFailed {
		t.Fatalf("run status = %s, want FAILED", runs[0].Status)
	}
	if runs[0].CheckName != "build" {
		t.Fatalf("CheckName = %q, want build", runs[0].CheckName)
	}
	if runs[0].Details != "... (head truncated, showing tail)\n6789" {
		t.Fatalf("Details = %q", runs[0].Details)
	}
}

func TestWait_UsesExplicitRequiredChecksFallbackWithoutTrackerRequirements(t *testing.T) {
	store := openTestStore(t)
	seedIssueWithPR(t, store, "exec-3", "25")

	trk := &stubTracker{
		checkResponses: [][]tracker.PullRequestCheck{
			{
				{Name: "lint", State: tracker.CheckSuccess},
				{Name: "optional", State: tracker.CheckFailure, Details: "still ignored"},
			},
		},
	}

	cfg := config.Default()
	cfg.CI.MergeRequirements.Mode = config.MergeRequirementsExplicit
	cfg.CI.MergeRequirements.Checks = []string{"lint"}
	supervisor := ci.New(store, trk, cfg, "main")
	supervisor.Now = func() time.Time { return time.Date(2026, 8, 28, 12, 4, 0, 0, time.UTC) }

	state, err := supervisor.Wait(context.Background(), "exec-3", "25")
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if state != domain.StateDone {
		t.Fatalf("state = %s, want DONE", state)
	}
	if trk.mergeCalls != 0 {
		t.Fatalf("GetMergeRequirements calls = %d, want 0 in explicit mode", trk.mergeCalls)
	}
}

func TestWait_TrackerErrorIsReturnedWithoutTransition(t *testing.T) {
	store := openTestStore(t)
	seedIssueWithPR(t, store, "exec-4", "26")

	trk := &stubTracker{mergeErr: errors.New("github exploded")}
	supervisor := ci.New(store, trk, config.Default(), "main")

	if _, err := supervisor.Wait(context.Background(), "exec-4", "26"); err == nil {
		t.Fatal("Wait: want error, got nil")
	}

	issue, err := store.GetIssue(context.Background(), "exec-4", "26")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.State != domain.StateCIPending {
		t.Fatalf("persisted state = %s, want CI_PENDING after tracker error", issue.State)
	}
}
