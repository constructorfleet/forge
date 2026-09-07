package wayfinding_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/needsinfo"
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

	events, err := store.EventsByExecution(ctx, "plan-exec-1")
	if err != nil {
		t.Fatalf("EventsByExecution: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("EventsByExecution = %v, want 1 event", events)
	}
	if events[0].Type != "decision.resumed" {
		t.Errorf("events[0].Type = %q, want %q", events[0].Type, "decision.resumed")
	}
	if !strings.Contains(events[0].Data, "001-vendor") {
		t.Errorf("events[0].Data = %q, want it to contain decision id %q", events[0].Data, "001-vendor")
	}
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

func TestResumeDecision_SharedAccountHumanReply_Resumes(t *testing.T) {
	store := openResumeTestStore(t)
	seedResumeExecution(t, store, "plan-exec-1", "42")
	trackerDouble := newFakeResumeTracker()

	checkpointTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	seedDecisionCheckpoint(t, store, "plan-exec-1", "001-vendor", "Which vendor?", checkpointTime, "shared-bot")

	trackerDouble.comments["42"] = []tracker.Comment{
		{
			Author:    "shared-bot",
			Body:      needsinfo.CommentMarker(needsinfo.KindNeedsHuman, "plan-exec-1", "001-vendor") + "\n\nWhich vendor?",
			CreatedAt: checkpointTime,
		},
		// Posted from the same shared account as forge's own comment, but
		// this is a genuine human answer -- it carries no forge marker.
		{Author: "shared-bot", Body: "Use vendor A", CreatedAt: checkpointTime.Add(time.Hour)},
	}

	ctx := context.Background()
	result, err := wayfinding.ResumeDecision(ctx, store, trackerDouble, "plan-exec-1", "001-vendor", time.Now)
	if err != nil {
		t.Fatalf("ResumeDecision: %v", err)
	}
	if !result.Resumed {
		t.Fatal("Resumed = false, want true: a human answer posted from forge's own account must not be dropped")
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

// invalidIDResumeTracker reports the Feature id is not a tracker issue,
// simulating a local Feature slug given to a numeric-issue provider.
type invalidIDResumeTracker struct{}

func (invalidIDResumeTracker) GetComments(_ context.Context, id string) ([]tracker.Comment, error) {
	return nil, fmt.Errorf("gitea: invalid issue id %q: %w", id, tracker.ErrInvalidIssueID)
}

// TestResumeDecision_InvalidIssueID_StaysNeedsHumanNoError proves a
// tracker-comment poll for a local Feature slug is a harmless no-op: the
// invalid-id error does not crash the poll, and the execution stays
// NEEDS_HUMAN until the local answer channel resumes it.
func TestResumeDecision_InvalidIssueID_StaysNeedsHumanNoError(t *testing.T) {
	store := openResumeTestStore(t)
	seedResumeExecution(t, store, "plan-exec-1", "autoapply")
	checkpointTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	seedDecisionCheckpoint(t, store, "plan-exec-1", "001-src", "Which source?", checkpointTime, "forge-bot")

	ctx := context.Background()
	result, err := wayfinding.ResumeDecision(ctx, store, invalidIDResumeTracker{}, "plan-exec-1", "001-src", time.Now)
	if err != nil {
		t.Fatalf("ResumeDecision must not fail on an invalid issue id: %v", err)
	}
	if result.Resumed {
		t.Fatal("Resumed = true, want false for a local Feature with no tracker comments")
	}
	exec, err := store.LoadPlanningExecution(ctx, "plan-exec-1")
	if err != nil {
		t.Fatalf("LoadPlanningExecution: %v", err)
	}
	if exec.Status != domain.PlanningStatusNeedsHuman {
		t.Errorf("Status = %q, want NEEDS_HUMAN", exec.Status)
	}
}

// TestAnswerDecisionLocally_RecordsAnswerAndSetsActive proves the local answer
// channel resumes a paused Decision without a tracker: it records the answer
// as the resumed context and transitions the execution to ACTIVE.
func TestAnswerDecisionLocally_RecordsAnswerAndSetsActive(t *testing.T) {
	store := openResumeTestStore(t)
	seedResumeExecution(t, store, "plan-exec-1", "autoapply")
	checkpointTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	seedDecisionCheckpoint(t, store, "plan-exec-1", "001-src", "Which source?", checkpointTime, "forge-bot")

	ctx := context.Background()
	result, err := wayfinding.AnswerDecisionLocally(ctx, store, "plan-exec-1", "001-src", "Use the SQL warehouse", time.Now)
	if err != nil {
		t.Fatalf("AnswerDecisionLocally: %v", err)
	}
	if !result.Resumed {
		t.Fatal("Resumed = false, want true")
	}

	exec, err := store.LoadPlanningExecution(ctx, "plan-exec-1")
	if err != nil {
		t.Fatalf("LoadPlanningExecution: %v", err)
	}
	if exec.Status != domain.PlanningStatusActive {
		t.Errorf("Status = %q, want ACTIVE", exec.Status)
	}

	checkpoint, err := store.GetDecisionCheckpoint(ctx, "plan-exec-1", "001-src")
	if err != nil {
		t.Fatalf("GetDecisionCheckpoint: %v", err)
	}
	if checkpoint.ResumedAt == nil {
		t.Error("checkpoint.ResumedAt is nil, want set")
	}
	if !strings.Contains(checkpoint.ResumedContext, "Use the SQL warehouse") {
		t.Errorf("ResumedContext = %q, want it to carry the answer", checkpoint.ResumedContext)
	}

	// A second answer is a no-op once resumed, not a double-record.
	again, err := wayfinding.AnswerDecisionLocally(ctx, store, "plan-exec-1", "001-src", "changed my mind", time.Now)
	if err != nil {
		t.Fatalf("AnswerDecisionLocally (second): %v", err)
	}
	if again.Resumed {
		t.Error("second answer Resumed = true, want false (already resumed)")
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
		{
			Author:    "forge-bot",
			Body:      needsinfo.CommentMarker(needsinfo.KindNeedsHuman, "plan-exec-1", "001-vendor") + "\n\noriginal comment",
			CreatedAt: trackerTime,
		},
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
