package tracker_test

import (
	"context"
	"testing"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/tracker"
)

func TestFakeTracker_CreateIssue_ThenGetIssue(t *testing.T) {
	trk := tracker.NewFakeTracker()

	created, err := trk.CreateIssue(context.Background(), tracker.IssueRequest{Title: "New Issue", Body: "desc"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected a non-empty ID")
	}

	fetched, err := trk.GetIssue(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if fetched.ID != created.ID || fetched.Title != "New Issue" {
		t.Fatalf("fetched %+v does not match created %+v", fetched, created)
	}
}

func TestFakeTracker_CreateIssue_AssignsDistinctIDs(t *testing.T) {
	trk := tracker.NewFakeTracker()

	first, err := trk.CreateIssue(context.Background(), tracker.IssueRequest{Title: "First"})
	if err != nil {
		t.Fatalf("CreateIssue(first): %v", err)
	}
	second, err := trk.CreateIssue(context.Background(), tracker.IssueRequest{Title: "Second"})
	if err != nil {
		t.Fatalf("CreateIssue(second): %v", err)
	}
	if first.ID == second.ID {
		t.Fatalf("expected distinct IDs, got %q twice", first.ID)
	}
}

func TestFakeTracker_GetIssue_UnknownIDErrors(t *testing.T) {
	trk := tracker.NewFakeTracker()
	if _, err := trk.GetIssue(context.Background(), "missing"); err == nil {
		t.Fatal("expected an error for an unknown issue ID")
	}
}

func TestFakeTracker_AddIssue_SeedsAFetchableIssue(t *testing.T) {
	trk := tracker.NewFakeTracker()
	trk.AddIssue(domain.Issue{ID: "seeded", Title: "Seeded Issue"})

	fetched, err := trk.GetIssue(context.Background(), "seeded")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if fetched.Title != "Seeded Issue" {
		t.Fatalf("got Title %q, want %q", fetched.Title, "Seeded Issue")
	}
}

func TestFakeTracker_Capabilities_DefaultsToZeroValue(t *testing.T) {
	trk := tracker.NewFakeTracker()
	if trk.Capabilities().PlanningMirror {
		t.Fatal("expected PlanningMirror false by default")
	}

	trk.SetCapabilities(tracker.Capabilities{PlanningMirror: true})
	if !trk.Capabilities().PlanningMirror {
		t.Fatal("expected SetCapabilities to take effect")
	}
}

func TestFakeTracker_AddLabel_IsIdempotent(t *testing.T) {
	trk := tracker.NewFakeTracker()
	ctx := context.Background()

	if err := trk.AddLabel(ctx, "1", "ready"); err != nil {
		t.Fatalf("AddLabel: %v", err)
	}
	if err := trk.AddLabel(ctx, "1", "ready"); err != nil {
		t.Fatalf("AddLabel (repeat): %v", err)
	}
	if got := trk.Labels("1"); len(got) != 1 {
		t.Fatalf("got labels %v, want exactly one", got)
	}
}

func TestFakeTracker_RemoveLabel_IsIdempotent(t *testing.T) {
	trk := tracker.NewFakeTracker()
	ctx := context.Background()

	if err := trk.AddLabel(ctx, "1", "ready"); err != nil {
		t.Fatalf("AddLabel: %v", err)
	}
	if err := trk.RemoveLabel(ctx, "1", "ready"); err != nil {
		t.Fatalf("RemoveLabel: %v", err)
	}
	if err := trk.RemoveLabel(ctx, "1", "ready"); err != nil {
		t.Fatalf("RemoveLabel (repeat): %v", err)
	}
	if got := trk.Labels("1"); len(got) != 0 {
		t.Fatalf("got labels %v, want none", got)
	}
}

func TestFakeTracker_CreatePullRequest_RecoversExistingByHead(t *testing.T) {
	trk := tracker.NewFakeTracker()
	ctx := context.Background()
	req := tracker.PullRequestRequest{Base: "main", Head: "feature-1", Title: "t"}

	first, err := trk.CreatePullRequest(ctx, req)
	if err != nil {
		t.Fatalf("CreatePullRequest: %v", err)
	}
	second, err := trk.CreatePullRequest(ctx, req)
	if err != nil {
		t.Fatalf("CreatePullRequest (repeat): %v", err)
	}
	if first.Number != second.Number {
		t.Fatalf("expected idempotent recovery, got %+v then %+v", first, second)
	}
}

func TestFakeTracker_AddComment_ThenGetComments(t *testing.T) {
	trk := tracker.NewFakeTracker()
	ctx := context.Background()

	if _, err := trk.AddComment(ctx, "1", "hello"); err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	comments, err := trk.GetComments(ctx, "1")
	if err != nil {
		t.Fatalf("GetComments: %v", err)
	}
	if len(comments) != 1 || comments[0].Body != "hello" {
		t.Fatalf("got comments %+v", comments)
	}
}
