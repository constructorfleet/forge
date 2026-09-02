package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
)

func TestReviewOverridesByIssue_ReturnsEmptyWhenNoneRecorded(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	got, err := store.ReviewOverridesByIssue(ctx, "issue-no-overrides")
	if err != nil {
		t.Fatalf("ReviewOverridesByIssue: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d overrides, want 0", len(got))
	}
}

func TestRecordReviewOverride_RoundTrips(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	createdAt := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	override := domain.ReviewOverride{
		IssueID:   "issue-override-1",
		Signature: "bugs|ERROR|main.go:1|still broken",
		Axis:      "bugs",
		File:      "main.go",
		Line:      1,
		Message:   "still broken",
		Reason:    "non-convergent: identical finding repeated across review retries",
		CreatedAt: createdAt,
	}
	if err := store.RecordReviewOverride(ctx, override); err != nil {
		t.Fatalf("RecordReviewOverride: %v", err)
	}

	got, err := store.ReviewOverridesByIssue(ctx, "issue-override-1")
	if err != nil {
		t.Fatalf("ReviewOverridesByIssue: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d overrides, want 1", len(got))
	}
	if got[0].Signature != override.Signature || got[0].Axis != override.Axis || got[0].File != override.File ||
		got[0].Line != override.Line || got[0].Message != override.Message || got[0].Reason != override.Reason {
		t.Errorf("got = %+v, want %+v", got[0], override)
	}
	if !got[0].CreatedAt.Equal(createdAt) {
		t.Errorf("CreatedAt = %v, want %v", got[0].CreatedAt, createdAt)
	}
}

func TestRecordReviewOverride_SameSignatureTwiceStaysOneRow(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	override := domain.ReviewOverride{
		IssueID:   "issue-override-2",
		Signature: "bugs|ERROR|main.go:1|still broken",
		Axis:      "bugs",
		File:      "main.go",
		Line:      1,
		Message:   "still broken",
		Reason:    "first escalation",
		CreatedAt: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
	}
	if err := store.RecordReviewOverride(ctx, override); err != nil {
		t.Fatalf("RecordReviewOverride (1st): %v", err)
	}
	if err := store.RecordReviewOverride(ctx, override); err != nil {
		t.Fatalf("RecordReviewOverride (2nd): %v", err)
	}

	got, err := store.ReviewOverridesByIssue(ctx, "issue-override-2")
	if err != nil {
		t.Fatalf("ReviewOverridesByIssue: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d overrides, want 1 (duplicate signature must not insert a second row)", len(got))
	}
}

func TestReviewOverridesByIssue_ScopedToIssueID(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	if err := store.RecordReviewOverride(ctx, domain.ReviewOverride{
		IssueID:   "issue-override-a",
		Signature: "sig-a",
		Axis:      "bugs",
		Message:   "a",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("RecordReviewOverride a: %v", err)
	}
	if err := store.RecordReviewOverride(ctx, domain.ReviewOverride{
		IssueID:   "issue-override-b",
		Signature: "sig-b",
		Axis:      "bugs",
		Message:   "b",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("RecordReviewOverride b: %v", err)
	}

	got, err := store.ReviewOverridesByIssue(ctx, "issue-override-a")
	if err != nil {
		t.Fatalf("ReviewOverridesByIssue: %v", err)
	}
	if len(got) != 1 || got[0].Signature != "sig-a" {
		t.Fatalf("got = %+v, want only issue-override-a's override", got)
	}
}
