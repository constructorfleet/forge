package planengine_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/planengine"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
)

func openTestStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "forge.db")
	store, err := storage.Open(dsn)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return store
}

var testExecutionSeq int

func newTestRuntime(store *storage.SQLiteStore) *planengine.Runtime {
	r := planengine.New(store)
	r.NewExecutionID = func() string {
		testExecutionSeq++
		return fmt.Sprintf("plan-exec-%d", testExecutionSeq)
	}
	r.Now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	r.OwnerPID = func() int { return 100 }
	r.ProcessRunning = func(pid int) (bool, error) { return false, nil }
	return r
}

func TestStartCreatesFreshPlanningExecution(t *testing.T) {
	store := openTestStore(t)
	r := newTestRuntime(store)

	exec, err := r.Start(context.Background(), "feature-1", "base-rev")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if exec.FeatureID != "feature-1" || exec.BaseRevision != "base-rev" {
		t.Fatalf("unexpected execution: %+v", exec)
	}
	if exec.Status != domain.PlanningStatusActive {
		t.Fatalf("expected ACTIVE status, got %s", exec.Status)
	}

	lease, err := store.FeaturePlanningLease(context.Background(), "feature-1")
	if err != nil {
		t.Fatalf("FeaturePlanningLease: %v", err)
	}
	if lease.ExecutionID != exec.ID {
		t.Fatalf("expected lease to reference %s, got %s", exec.ID, lease.ExecutionID)
	}
	if lease.OwnerPID != 100 {
		t.Fatalf("expected owner pid 100, got %d", lease.OwnerPID)
	}
}

func TestStartRejectsWhileLiveProcessOwnsLease(t *testing.T) {
	store := openTestStore(t)
	r := newTestRuntime(store)

	first, err := r.Start(context.Background(), "feature-1", "base-rev")
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}

	other := newTestRuntime(store)
	other.OwnerPID = func() int { return 200 }
	other.ProcessRunning = func(pid int) (bool, error) { return true, nil } // pid 100 still alive

	_, err = other.Start(context.Background(), "feature-1", "base-rev")
	if err == nil {
		t.Fatal("expected an error starting planning while a live process owns the lease")
	}
	var conflict *storage.PlanningLeaseConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected *storage.PlanningLeaseConflictError, got %v", err)
	}
	if conflict.OwningExecutionID != first.ID {
		t.Fatalf("expected conflict to reference %s, got %s", first.ID, conflict.OwningExecutionID)
	}
}

func TestStartReclaimsAbandonedLease(t *testing.T) {
	store := openTestStore(t)
	r := newTestRuntime(store)

	first, err := r.Start(context.Background(), "feature-1", "base-rev")
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}

	resumer := newTestRuntime(store)
	resumer.OwnerPID = func() int { return 999 }
	resumer.ProcessRunning = func(pid int) (bool, error) { return false, nil } // pid 100 is dead

	resumed, err := resumer.Start(context.Background(), "feature-1", "base-rev")
	if err != nil {
		t.Fatalf("resume Start: %v", err)
	}
	if resumed.ID != first.ID {
		t.Fatalf("expected resumed execution to reuse %s, got %s", first.ID, resumed.ID)
	}

	lease, err := store.FeaturePlanningLease(context.Background(), "feature-1")
	if err != nil {
		t.Fatalf("FeaturePlanningLease: %v", err)
	}
	if lease.OwnerPID != 999 {
		t.Fatalf("expected lease reclaimed by pid 999, got %d", lease.OwnerPID)
	}
}

func TestStartTreatsOwnRestartAsReclaim(t *testing.T) {
	store := openTestStore(t)
	r := newTestRuntime(store)

	first, err := r.Start(context.Background(), "feature-1", "base-rev")
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}

	// Same process ID restarting: ProcessRunning would report itself alive,
	// but since OwnerPID matches, it's a restart, not a foreign live owner.
	restarted := newTestRuntime(store)
	restarted.OwnerPID = func() int { return 100 }
	restarted.ProcessRunning = func(pid int) (bool, error) { return true, nil }

	exec, err := restarted.Start(context.Background(), "feature-1", "base-rev")
	if err != nil {
		t.Fatalf("restart Start: %v", err)
	}
	if exec.ID != first.ID {
		t.Fatalf("expected restart to reuse %s, got %s", first.ID, exec.ID)
	}
}

func TestStartAfterFinishStartsFreshExecution(t *testing.T) {
	store := openTestStore(t)
	r := newTestRuntime(store)

	first, err := r.Start(context.Background(), "feature-1", "base-rev")
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := r.Finish(context.Background(), "feature-1", first.ID, domain.PlanningStatusComplete); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	second, err := r.Start(context.Background(), "feature-1", "base-rev-2")
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if second.ID == first.ID {
		t.Fatal("expected a fresh execution after Finish, got the same ID")
	}

	if _, err := store.FeaturePlanningLease(context.Background(), "feature-1"); err != nil {
		t.Fatalf("expected an active lease for the fresh execution: %v", err)
	}
}

func TestStartReleasesLeaseFromTerminalExecutionStillHeld(t *testing.T) {
	store := openTestStore(t)
	r := newTestRuntime(store)

	first, err := r.Start(context.Background(), "feature-1", "base-rev")
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	// Mark the execution terminal without releasing the lease directly
	// (simulating a crash between UpdatePlanningStatus and lease release).
	if err := store.UpdatePlanningStatus(context.Background(), first.ID, domain.PlanningStatusFailed); err != nil {
		t.Fatalf("UpdatePlanningStatus: %v", err)
	}

	resumer := newTestRuntime(store)
	resumer.OwnerPID = func() int { return 999 }
	resumer.ProcessRunning = func(pid int) (bool, error) { return false, nil }

	second, err := resumer.Start(context.Background(), "feature-1", "base-rev-2")
	if err != nil {
		t.Fatalf("Start after terminal: %v", err)
	}
	if second.ID == first.ID {
		t.Fatal("expected a fresh execution once the prior one was terminal")
	}
	if second.Status != domain.PlanningStatusActive {
		t.Fatalf("expected fresh execution to be ACTIVE, got %s", second.Status)
	}
}

func TestFinishReleasesLeaseAndPersistsStatus(t *testing.T) {
	store := openTestStore(t)
	r := newTestRuntime(store)

	exec, err := r.Start(context.Background(), "feature-1", "base-rev")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := r.Finish(context.Background(), "feature-1", exec.ID, domain.PlanningStatusNeedsHuman); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	loaded, err := store.LoadPlanningExecution(context.Background(), exec.ID)
	if err != nil {
		t.Fatalf("LoadPlanningExecution: %v", err)
	}
	if loaded.Status != domain.PlanningStatusNeedsHuman {
		t.Fatalf("expected NEEDS_HUMAN, got %s", loaded.Status)
	}
	if _, err := store.FeaturePlanningLease(context.Background(), "feature-1"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected lease released, got %v", err)
	}
}

// fakeTrackerForResume is a minimal tracker for resume tests.
type fakeTrackerForResume struct {
	comments map[string][]tracker.Comment
}

func newFakeTrackerForResume() *fakeTrackerForResume {
	return &fakeTrackerForResume{comments: map[string][]tracker.Comment{}}
}

func (f *fakeTrackerForResume) GetComments(_ context.Context, id string) ([]tracker.Comment, error) {
	return f.comments[id], nil
}

func (f *fakeTrackerForResume) AddComment(_ context.Context, id, body string) (tracker.Comment, error) {
	c := tracker.Comment{Author: "forge-bot", Body: body, CreatedAt: time.Now()}
	f.comments[id] = append(f.comments[id], c)
	return c, nil
}

func (f *fakeTrackerForResume) AddLabel(_ context.Context, _, _ string) error    { return nil }
func (f *fakeTrackerForResume) RemoveLabel(_ context.Context, _, _ string) error { return nil }
func (f *fakeTrackerForResume) GetIssue(_ context.Context, _ string) (domain.Issue, error) {
	return domain.Issue{}, storage.ErrNotFound
}
func (f *fakeTrackerForResume) GetIssues(_ context.Context, _ []string) ([]domain.Issue, error) {
	return nil, nil
}
func (f *fakeTrackerForResume) GetMergeRequirements(_ context.Context, _ string) (tracker.MergeRequirements, error) {
	return tracker.MergeRequirements{}, nil
}
func (f *fakeTrackerForResume) GetPullRequestChecks(_ context.Context, _ int) ([]tracker.PullRequestCheck, error) {
	return nil, nil
}
func (f *fakeTrackerForResume) CreatePullRequest(_ context.Context, _ tracker.PullRequestRequest) (tracker.PullRequest, error) {
	return tracker.PullRequest{}, nil
}
func (f *fakeTrackerForResume) CreateIssue(_ context.Context, _ tracker.IssueRequest) (tracker.CreatedIssue, error) {
	return tracker.CreatedIssue{}, nil
}
func (f *fakeTrackerForResume) UpdateIssue(_ context.Context, _ string, _ tracker.UpdateIssueRequest) error {
	return nil
}
func (f *fakeTrackerForResume) Capabilities() tracker.Capabilities { return tracker.Capabilities{} }

func TestResumePlanningExecution_ResumesWhenNewHumanComment(t *testing.T) {
	store := openTestStore(t)
	r := newTestRuntime(store)

	// Create a planning execution in NEEDS_HUMAN status
	exec, err := r.Start(context.Background(), "feature-1", "base-rev")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := r.Finish(context.Background(), "feature-1", exec.ID, domain.PlanningStatusNeedsHuman); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	// Seed a decision checkpoint for this execution
	decisionID := "001-vendor"
	question := "Which vendor?"
	checkpointTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	// We need to use storage.DecisionCheckpoint directly
	// Note: We can't easily create a real Decision artifact here, so we'll
	// use the storage layer directly
	checkpoint := storage.DecisionCheckpoint{
		ExecutionID:      exec.ID,
		DecisionID:       decisionID,
		DecisionRevision: "rev1",
		Question:         question,
		Context:          "some context",
		LabelAdded:       true,
		CommentPosted:    true,
		CommentAuthor:    "forge-bot",
		CommentPostedAt:  checkpointTime,
		CreatedAt:        time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC),
	}
	if err := store.SaveDecisionCheckpoint(context.Background(), checkpoint); err != nil {
		t.Fatalf("SaveDecisionCheckpoint: %v", err)
	}

	// Set up tracker with a new human comment after the checkpoint
	trackerDouble := newFakeTrackerForResume()
	trackerDouble.comments["feature-1"] = []tracker.Comment{
		{Author: "forge-bot", Body: "original comment", CreatedAt: checkpointTime},
		{Author: "alice", Body: "Use vendor A", CreatedAt: checkpointTime.Add(time.Hour)},
	}

	// Resume the planning execution
	resumedExec, anyResumed, err := r.ResumePlanningExecution(context.Background(), exec.ID, trackerDouble)
	if err != nil {
		t.Fatalf("ResumePlanningExecution: %v", err)
	}
	if !anyResumed {
		t.Fatal("expected anyResumed = true")
	}
	if resumedExec.Status != domain.PlanningStatusActive {
		t.Errorf("expected ACTIVE status, got %s", resumedExec.Status)
	}

	// Check the checkpoint was updated with resumed context
	updatedCheckpoint, err := store.GetDecisionCheckpoint(context.Background(), exec.ID, decisionID)
	if err != nil {
		t.Fatalf("GetDecisionCheckpoint: %v", err)
	}
	if updatedCheckpoint.ResumedAt == nil {
		t.Error("checkpoint.ResumedAt is nil, want set")
	}
	if updatedCheckpoint.ResumedContext == "" {
		t.Error("checkpoint.ResumedContext is empty, want the serialized resumed context")
	}
}

func TestResumePlanningExecution_NoNewComment_StaysNeedsHuman(t *testing.T) {
	store := openTestStore(t)
	r := newTestRuntime(store)

	exec, err := r.Start(context.Background(), "feature-1", "base-rev")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := r.Finish(context.Background(), "feature-1", exec.ID, domain.PlanningStatusNeedsHuman); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	decisionID := "001-vendor"
	question := "Which vendor?"
	checkpointTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	checkpoint := storage.DecisionCheckpoint{
		ExecutionID:      exec.ID,
		DecisionID:       decisionID,
		DecisionRevision: "rev1",
		Question:         question,
		Context:          "some context",
		LabelAdded:       true,
		CommentPosted:    true,
		CommentAuthor:    "forge-bot",
		CommentPostedAt:  checkpointTime,
		CreatedAt:        time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC),
	}
	if err := store.SaveDecisionCheckpoint(context.Background(), checkpoint); err != nil {
		t.Fatalf("SaveDecisionCheckpoint: %v", err)
	}

	trackerDouble := newFakeTrackerForResume()
	trackerDouble.comments["feature-1"] = []tracker.Comment{
		{Author: "forge-bot", Body: "original comment", CreatedAt: checkpointTime},
	}

	resumedExec, anyResumed, err := r.ResumePlanningExecution(context.Background(), exec.ID, trackerDouble)
	if err != nil {
		t.Fatalf("ResumePlanningExecution: %v", err)
	}
	if anyResumed {
		t.Fatal("expected anyResumed = false (no new human comment)")
	}
	if resumedExec.Status != domain.PlanningStatusNeedsHuman {
		t.Errorf("expected NEEDS_HUMAN status, got %s", resumedExec.Status)
	}
}
