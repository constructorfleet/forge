package storage_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
)

func openDecisionTestStore(t *testing.T) *storage.SQLiteStore {
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

func seedDecisionExecution(t *testing.T, store *storage.SQLiteStore, executionID, featureID string) {
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

func TestDecisionCheckpoint_SaveAndGet_RoundTrips(t *testing.T) {
	store := openDecisionTestStore(t)
	seedDecisionExecution(t, store, "plan-exec-1", "42")

	checkpoint := storage.DecisionCheckpoint{
		ExecutionID:      "plan-exec-1",
		DecisionID:       "001-storage",
		DecisionRevision: "abc123",
		Question:         "Which storage backend?",
		Context:          "Need something lightweight.",
		LabelAdded:       true,
		CommentPosted:    true,
		CommentAuthor:    "forge-bot",
		CommentPostedAt:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		CreatedAt:        time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	if err := store.SaveDecisionCheckpoint(context.Background(), checkpoint); err != nil {
		t.Fatalf("SaveDecisionCheckpoint: %v", err)
	}

	got, err := store.GetDecisionCheckpoint(context.Background(), "plan-exec-1", "001-storage")
	if err != nil {
		t.Fatalf("GetDecisionCheckpoint: %v", err)
	}

	if got.ExecutionID != checkpoint.ExecutionID ||
		got.DecisionID != checkpoint.DecisionID ||
		got.DecisionRevision != checkpoint.DecisionRevision ||
		got.Question != checkpoint.Question ||
		got.Context != checkpoint.Context ||
		got.LabelAdded != checkpoint.LabelAdded ||
		got.CommentPosted != checkpoint.CommentPosted ||
		got.CommentAuthor != checkpoint.CommentAuthor ||
		!got.CommentPostedAt.Equal(checkpoint.CommentPostedAt) ||
		!got.CreatedAt.Equal(checkpoint.CreatedAt) {
		t.Errorf("GetDecisionCheckpoint = %+v, want %+v", got, checkpoint)
	}
}

func TestDecisionCheckpoint_Save_UpsertsExistingRow(t *testing.T) {
	store := openDecisionTestStore(t)
	seedDecisionExecution(t, store, "plan-exec-1", "42")

	intent := storage.DecisionCheckpoint{
		ExecutionID:      "plan-exec-1",
		DecisionID:       "001-storage",
		DecisionRevision: "abc123",
		Question:         "Which storage backend?",
		LabelAdded:       true,
		CommentPosted:    false,
		CreatedAt:        time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := store.SaveDecisionCheckpoint(context.Background(), intent); err != nil {
		t.Fatalf("SaveDecisionCheckpoint (intent): %v", err)
	}

	posted := storage.DecisionCheckpoint{
		ExecutionID:      "plan-exec-1",
		DecisionID:       "001-storage",
		DecisionRevision: "abc123",
		Question:         "Which storage backend?",
		LabelAdded:       true,
		CommentPosted:    true,
		CommentAuthor:    "forge-bot",
		CommentPostedAt:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		CreatedAt:        time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := store.SaveDecisionCheckpoint(context.Background(), posted); err != nil {
		t.Fatalf("SaveDecisionCheckpoint (posted): %v", err)
	}

	got, err := store.GetDecisionCheckpoint(context.Background(), "plan-exec-1", "001-storage")
	if err != nil {
		t.Fatalf("GetDecisionCheckpoint: %v", err)
	}
	if !got.CommentPosted || got.CommentAuthor != "forge-bot" {
		t.Errorf("checkpoint = %+v, want CommentPosted=true and CommentAuthor=forge-bot", got)
	}
}

func TestDecisionCheckpoint_Get_ReturnsNotFound(t *testing.T) {
	store := openDecisionTestStore(t)
	seedDecisionExecution(t, store, "plan-exec-1", "42")

	_, err := store.GetDecisionCheckpoint(context.Background(), "plan-exec-1", "001-storage")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("GetDecisionCheckpoint error = %v, want ErrNotFound", err)
	}
}