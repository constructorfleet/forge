package wayfinding_test

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/decisiongraph"
	"github.com/Teagan42/forge/internal/decisionresolution"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/needsinfo"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
	"github.com/Teagan42/forge/internal/wayfinding"
)

// fakePauseTracker is an in-memory wayfinding.NeedsHumanTracker double,
// mirroring internal/engine's fakeTracker: it records every AddLabel/
// AddComment call so tests can assert idempotency without hitting a real
// tracker.
type fakePauseTracker struct {
	mu       sync.Mutex
	labels   map[string][]string
	comments map[string][]tracker.Comment
}

func newFakePauseTracker() *fakePauseTracker {
	return &fakePauseTracker{labels: map[string][]string{}, comments: map[string][]tracker.Comment{}}
}

func (f *fakePauseTracker) AddLabel(_ context.Context, id string, label string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, l := range f.labels[id] {
		if l == label {
			return nil
		}
	}
	f.labels[id] = append(f.labels[id], label)
	return nil
}

func (f *fakePauseTracker) AddComment(_ context.Context, id string, body string) (tracker.Comment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := tracker.Comment{Author: "forge-bot", Body: body, CreatedAt: time.Now()}
	f.comments[id] = append(f.comments[id], c)
	return c, nil
}

func (f *fakePauseTracker) CommentCount(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.comments[id])
}

func (f *fakePauseTracker) Labels(id string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.labels[id]...)
}

func openPauseTestStore(t *testing.T) *storage.SQLiteStore {
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

func seedPauseExecution(t *testing.T, store *storage.SQLiteStore, executionID, featureID string) {
	t.Helper()
	exec := domain.PlanningExecution{
		ID:           executionID,
		FeatureID:    featureID,
		BaseRevision: "base",
		Status:       domain.PlanningStatusActive,
		StartedAt:    time.Now(),
	}
	if err := store.CreatePlanningExecution(context.Background(), exec); err != nil {
		t.Fatalf("CreatePlanningExecution: %v", err)
	}
}

func decisionWithQuestion(question string) *planning.Artifact {
	d := &planning.Artifact{Kind: planning.KindDecision, Sections: []planning.Section{{Heading: "Question", Body: question}}}
	d.Revision = planning.ComputeRevision(d)
	return d
}

func TestPauseHandler_Handle_PostsChecksAndSetsStatus(t *testing.T) {
	store := openPauseTestStore(t)
	seedPauseExecution(t, store, "plan-exec-1", "42")
	trackerDouble := newFakePauseTracker()

	handler := &wayfinding.PauseHandler{
		ExecutionID: "plan-exec-1",
		FeatureID:   "42",
		Store:       store,
		Tracker:     trackerDouble,
		Label:       "needs-info",
		PostComment: true,
		Now:         func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}

	decision := decisionWithQuestion("Which vendor do we pick?")
	detail := decisionresolution.NeedsHumanDetail{Question: "Which vendor do we pick?", Context: "Both meet requirements."}

	paused, err := handler.Handle(context.Background(), "001-vendor", decision, detail)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if paused.State != decisiongraph.StateNeedsHuman {
		t.Errorf("paused.State = %q, want %q", paused.State, decisiongraph.StateNeedsHuman)
	}
	if planning.Ready(paused) {
		t.Error("paused Decision must not be Ready")
	}

	if got := trackerDouble.Labels("42"); len(got) != 1 || got[0] != "needs-info" {
		t.Errorf("Labels(42) = %v, want [needs-info]", got)
	}
	if got := trackerDouble.CommentCount("42"); got != 1 {
		t.Errorf("CommentCount(42) = %d, want 1", got)
	}
	posted := trackerDouble.comments["42"][0]
	wantMarker := needsinfo.CommentMarker(needsinfo.KindNeedsHuman, "plan-exec-1", "001-vendor")
	if !strings.Contains(posted.Body, wantMarker) {
		t.Errorf("posted comment body = %q, want it to contain marker %q", posted.Body, wantMarker)
	}

	checkpoint, err := store.GetDecisionCheckpoint(context.Background(), "plan-exec-1", "001-vendor")
	if err != nil {
		t.Fatalf("GetDecisionCheckpoint: %v", err)
	}
	if checkpoint.Question != detail.Question || checkpoint.Context != detail.Context {
		t.Errorf("checkpoint = %+v, want question/context to match detail", checkpoint)
	}
	if !checkpoint.LabelAdded || !checkpoint.CommentPosted {
		t.Errorf("checkpoint = %+v, want LabelAdded and CommentPosted true", checkpoint)
	}
	if checkpoint.DecisionRevision != decision.Revision {
		t.Errorf("checkpoint.DecisionRevision = %q, want %q", checkpoint.DecisionRevision, decision.Revision)
	}

	exec, err := store.LoadPlanningExecution(context.Background(), "plan-exec-1")
	if err != nil {
		t.Fatalf("LoadPlanningExecution: %v", err)
	}
	if exec.Status != domain.PlanningStatusNeedsHuman {
		t.Errorf("Status = %q, want NEEDS_HUMAN", exec.Status)
	}

	events, err := store.EventsByExecution(context.Background(), "plan-exec-1")
	if err != nil {
		t.Fatalf("EventsByExecution: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("EventsByExecution = %v, want 1 event", events)
	}
	if events[0].Type != "decision.paused" {
		t.Errorf("events[0].Type = %q, want %q", events[0].Type, "decision.paused")
	}
	if !strings.Contains(events[0].Data, "001-vendor") {
		t.Errorf("events[0].Data = %q, want it to contain decision id %q", events[0].Data, "001-vendor")
	}
}

func TestPauseHandler_Handle_IsIdempotent(t *testing.T) {
	store := openPauseTestStore(t)
	seedPauseExecution(t, store, "plan-exec-1", "42")
	trackerDouble := newFakePauseTracker()

	handler := &wayfinding.PauseHandler{
		ExecutionID: "plan-exec-1",
		FeatureID:   "42",
		Store:       store,
		Tracker:     trackerDouble,
		Label:       "needs-info",
		PostComment: true,
	}

	decision := decisionWithQuestion("Which vendor do we pick?")
	detail := decisionresolution.NeedsHumanDetail{Question: "Which vendor do we pick?"}

	if _, err := handler.Handle(context.Background(), "001-vendor", decision, detail); err != nil {
		t.Fatalf("Handle (1st): %v", err)
	}
	if _, err := handler.Handle(context.Background(), "001-vendor", decision, detail); err != nil {
		t.Fatalf("Handle (2nd): %v", err)
	}

	if got := trackerDouble.CommentCount("42"); got != 1 {
		t.Errorf("CommentCount(42) = %d, want 1 (re-running must not double-post)", got)
	}

	events, err := store.EventsByExecution(context.Background(), "plan-exec-1")
	if err != nil {
		t.Fatalf("EventsByExecution: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("EventsByExecution = %v, want 1 event (re-running must not double-record)", events)
	}
}

func TestPauseHandler_Handle_NilTrackerSkipsPostingButStillCheckpoints(t *testing.T) {
	store := openPauseTestStore(t)
	seedPauseExecution(t, store, "plan-exec-1", "42")

	handler := &wayfinding.PauseHandler{
		ExecutionID: "plan-exec-1",
		FeatureID:   "42",
		Store:       store,
		Tracker:     nil,
		Label:       "needs-info",
		PostComment: true,
	}

	decision := decisionWithQuestion("Which vendor do we pick?")
	detail := decisionresolution.NeedsHumanDetail{Question: "Which vendor do we pick?"}

	if _, err := handler.Handle(context.Background(), "001-vendor", decision, detail); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	checkpoint, err := store.GetDecisionCheckpoint(context.Background(), "plan-exec-1", "001-vendor")
	if err != nil {
		t.Fatalf("GetDecisionCheckpoint: %v", err)
	}
	if checkpoint.LabelAdded || checkpoint.CommentPosted {
		t.Errorf("checkpoint = %+v, want LabelAdded and CommentPosted both false with a nil Tracker", checkpoint)
	}

	exec, err := store.LoadPlanningExecution(context.Background(), "plan-exec-1")
	if err != nil {
		t.Fatalf("LoadPlanningExecution: %v", err)
	}
	if exec.Status != domain.PlanningStatusNeedsHuman {
		t.Errorf("Status = %q, want NEEDS_HUMAN even without a tracker", exec.Status)
	}
}
