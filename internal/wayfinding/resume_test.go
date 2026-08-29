package wayfinding_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
	"github.com/Teagan42/forge/internal/wayfinding"
)

// fakeResumeTracker is an in-memory tracker double for resume tests.
type fakeResumeTracker struct {
	comments map[string][]tracker.Comment
}

func newFakeResumeTracker() *fakeResumeTracker {
	return &fakeResumeTracker{comments: map[string][]tracker.Comment{}}
}

func (f *fakeResumeTracker) GetComments(_ context.Context, id string) ([]tracker.Comment, error) {
	return f.comments[id], nil
}

func (f *fakeResumeTracker) AddComment(id, body string) (tracker.Comment, error) {
	c := tracker.Comment{Author: "forge-bot", Body: body, CreatedAt: time.Now()}
	f.comments[id] = append(f.comments[id], c)
	return c, nil
}

func openResumeTestStore(t *testing.T) *storage.SQLiteStore {
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

func seedResumeExecution(t *testing.T, store *storage.SQLiteStore, executionID, featureID string) {
	t.Helper()
	exec := domain.PlanningExecution{
		ID:           executionID,
		FeatureID:    featureID,
		BaseRevision: "base",
		Status:       domain.PlanningStatusNeedsHuman,
		StartedAt:    time.Now(),
	}
	if err := store.CreatePlanningExecution(context.Background(), exec); err != nil {
		t.Fatalf("CreatePlanningExecution: %v", err)
	}
}

func seedDecisionCheckpoint(t *testing.T, store *storage.SQLiteStore, executionID, decisionID, question string, commentPostedAt time.Time, commentAuthor string) {
	t.Helper()
	decision := &planning.Artifact{Kind: planning.KindDecision, Sections: []planning.Section{{Heading: "Question", Body: question}}}
	decision.Revision = planning.ComputeRevision(decision)
	checkpoint := storage.DecisionCheckpoint{
		ExecutionID:      executionID,
		DecisionID:       decisionID,
		DecisionRevision: decision.Revision,
		Question:         question,
		Context:          "some context",
		LabelAdded:       true,
		CommentPosted:    true,
		CommentAuthor:    commentAuthor,
		CommentPostedAt:  commentPostedAt,
		CreatedAt:        time.Now().Add(-time.Hour),
	}
	if err := store.SaveDecisionCheckpoint(context.Background(), checkpoint); err != nil {
		t.Fatalf("SaveDecisionCheckpoint: %v", err)
	}
}

func TestResumeDecision_NewHumanComment_ResumesAndSetsActive(t *testing.T) {
	store := openResumeTestStore(t)
	seedResumeExecution(t, store, "plan-exec-1", "42")
	trackerDouble := newFakeResumeTracker()

	checkpointTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	seedDecisionCheckpoint(t, store, "plan-exec-1", "001-vendor", "Which vendor?", checkpointTime, "forge-bot")

	trackerDouble.comments["42"] = []tracker.Comment{
		{Author: "forge-bot", Body: "original comment", CreatedAt: checkpointTime},
		{Author: "alice", Body: "Use vendor A", CreatedAt: checkpointTime.Add(time.Hour)},
	}

	ctx := context.Background()
	result, err := wayfinding.ResumeDecision(ctx, store, trackerDouble, "plan-exec-1", "001-vendor", time.Now)
	if err != nil {
		t.Fatalf("ResumeDecision: %v", err)
	}
	if !result.Resumed {
		t.Fatal("Resumed = false, want true")
	}
	exec, err := store.LoadPlanningExecution(ctx, "plan-exec-1")
	if err != nil {
		t.Fatalf("LoadPlanningExecution: %v", err)
	}
	if exec.Status != domain.PlanningStatusActive {
		t.Errorf("PlanningExecution.Status = %q, want ACTIVE", exec.Status)
	}

	// Check the checkpoint has the resumed context
	checkpoint, err := store.GetDecisionCheckpoint(ctx, "plan-exec-1", "001-vendor")
	if err != nil {
		t.Fatalf("GetDecisionCheckpoint: %v", err)
	}
	if checkpoint.ResumedAt == nil {
		t.Error("checkpoint.ResumedAt is nil, want set")
	}
	if checkpoint.ResumedContext == "" {
		t.Error("checkpoint.ResumedContext is empty, want the serialized resumed context")
	}

	// Note: decision.resumed event is not recorded to the events table
	// (which is for coding executions). A separate planning events table
	// would be needed for full parity with Phase 1's needsinfo.resumed event.
}

func TestResumeDecision_NoNewComment_StaysNeedsHuman(t *testing.T) {
	store := openResumeTestStore(t)
	seedResumeExecution(t, store, "plan-exec-1", "42")
	trackerDouble := newFakeResumeTracker()

	checkpointTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	seedDecisionCheckpoint(t, store, "plan-exec-1", "001-vendor", "Which vendor?", checkpointTime, "forge-bot")

	trackerDouble.comments["42"] = []tracker.Comment{
		{Author: "forge-bot", Body: "original comment", CreatedAt: checkpointTime},
	}

	ctx := context.Background()
	result, err := wayfinding.ResumeDecision(ctx, store, trackerDouble, "plan-exec-1", "001-vendor", time.Now)
	if err != nil {
		t.Fatalf("ResumeDecision: %v", err)
	}
	if result.Resumed {
		t.Fatal("Resumed = true, want false (no new human comment)")
	}
	exec, err := store.LoadPlanningExecution(ctx, "plan-exec-1")
	if err != nil {
		t.Fatalf("LoadPlanningExecution: %v", err)
	}
	if exec.Status != domain.PlanningStatusNeedsHuman {
		t.Errorf("PlanningExecution.Status = %q, want NEEDS_HUMAN", exec.Status)
	}
}

func TestResumeDecision_OlderComment_StaysNeedsHuman(t *testing.T) {
	store := openResumeTestStore(t)
	seedResumeExecution(t, store, "plan-exec-1", "42")
	trackerDouble := newFakeResumeTracker()

	checkpointTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	seedDecisionCheckpoint(t, store, "plan-exec-1", "001-vendor", "Which vendor?", checkpointTime, "forge-bot")

	trackerDouble.comments["42"] = []tracker.Comment{
		{Author: "forge-bot", Body: "original comment", CreatedAt: checkpointTime},
		{Author: "alice", Body: "old comment", CreatedAt: checkpointTime.Add(-time.Hour)},
	}

	ctx := context.Background()
	result, err := wayfinding.ResumeDecision(ctx, store, trackerDouble, "plan-exec-1", "001-vendor", time.Now)
	if err != nil {
		t.Fatalf("ResumeDecision: %v", err)
	}
	if result.Resumed {
		t.Fatal("Resumed = true, want false (comment is older than checkpoint)")
	}
}

func TestResumeDecision_NoCheckpoint_ReturnsError(t *testing.T) {
	store := openResumeTestStore(t)
	seedResumeExecution(t, store, "plan-exec-1", "42")
	trackerDouble := newFakeResumeTracker()

	ctx := context.Background()
	_, err := wayfinding.ResumeDecision(ctx, store, trackerDouble, "plan-exec-1", "001-vendor", time.Now)
	if err == nil {
		t.Fatal("ResumeDecision: want error for decision with no checkpoint, got nil")
	}
}

func TestResumeDecision_ClockSkewAndOwnComment_DoesNotFalseTrigger(t *testing.T) {
	store := openResumeTestStore(t)
	seedResumeExecution(t, store, "plan-exec-1", "42")
	trackerDouble := newFakeResumeTracker()

	localTime := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC) // 1 hour behind
	trackerTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	seedDecisionCheckpoint(t, store, "plan-exec-1", "001-vendor", "Which vendor?", trackerTime, "forge-bot")

	trackerDouble.comments["42"] = []tracker.Comment{
		{Author: "forge-bot", Body: "original comment", CreatedAt: trackerTime},
	}

	ctx := context.Background()
	result, err := wayfinding.ResumeDecision(ctx, store, trackerDouble, "plan-exec-1", "001-vendor", func() time.Time { return localTime })
	if err != nil {
		t.Fatalf("ResumeDecision: %v", err)
	}
	if result.Resumed {
		t.Fatal("Resumed = true, want false: only forge's own comment exists")
	}

	trackerDouble.comments["42"] = append(trackerDouble.comments["42"],
		tracker.Comment{Author: "alice", Body: "Use vendor A", CreatedAt: trackerTime.Add(time.Minute)},
	)

	result, err = wayfinding.ResumeDecision(ctx, store, trackerDouble, "plan-exec-1", "001-vendor", func() time.Time { return localTime })
	if err != nil {
		t.Fatalf("ResumeDecision: %v", err)
	}
	if !result.Resumed {
		t.Fatal("Resumed = false, want true after genuine human comment")
	}
}