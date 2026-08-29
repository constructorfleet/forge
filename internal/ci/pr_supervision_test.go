package ci_test

import (
	"context"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/ci"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
)

// stubTrackerWithMergeStatus adds tracker.MergeStatusGetter to stubTracker,
// letting a test opt a Tracker double into Wait's merge-conflict poll
// (internal/ci/conflict.go's pollConflict type-asserts for the capability)
// without affecting every other test's stubTracker, which deliberately
// does not implement it.
type stubTrackerWithMergeStatus struct {
	stubTracker
	conflicted bool
}

func (s *stubTrackerWithMergeStatus) GetPullRequestMergeStatus(context.Context, int) (tracker.PullRequestMergeStatus, error) {
	return tracker.PullRequestMergeStatus{Conflicted: s.conflicted}, nil
}

// stubTrackerWithReviews adds tracker.ReviewsGetter to stubTracker, same
// rationale as stubTrackerWithMergeStatus but for pollReviews.
type stubTrackerWithReviews struct {
	stubTracker
	reviews []tracker.PullRequestReview
}

func (s *stubTrackerWithReviews) GetPullRequestReviews(context.Context, int) ([]tracker.PullRequestReview, error) {
	return s.reviews, nil
}

// stubNeedsInfoTracker is a minimal ci.NeedsInfoTracker double.
type stubNeedsInfoTracker struct {
	labels   map[string][]string
	comments []string
}

func newStubNeedsInfoTracker() *stubNeedsInfoTracker {
	return &stubNeedsInfoTracker{labels: map[string][]string{}}
}

func (s *stubNeedsInfoTracker) AddLabel(_ context.Context, id, label string) error {
	s.labels[id] = append(s.labels[id], label)
	return nil
}

func (s *stubNeedsInfoTracker) AddComment(_ context.Context, id, body string) (tracker.Comment, error) {
	s.comments = append(s.comments, body)
	return tracker.Comment{Author: "forge-bot", CreatedAt: time.Date(2026, 8, 28, 12, 5, 0, 0, time.UTC)}, nil
}

func TestWait_MergeConflict_RoutesToNeedsInfo(t *testing.T) {
	store := openTestStore(t)
	seedIssueWithPR(t, store, "exec-conflict", "30")

	trk := &stubTrackerWithMergeStatus{
		stubTracker: stubTracker{mergeRequirements: tracker.MergeRequirements{RequiredChecks: []string{"build"}}},
		conflicted:  true,
	}

	cfg := config.Default()
	cfg.Blocked.Label = "forge-blocked"
	cfg.Blocked.Comment = true

	supervisor := ci.New(store, trk, cfg, "main")
	needsInfo := newStubNeedsInfoTracker()
	supervisor.NeedsInfoTracker = needsInfo
	supervisor.Now = func() time.Time { return time.Date(2026, 8, 28, 12, 6, 0, 0, time.UTC) }

	state, err := supervisor.Wait(context.Background(), "exec-conflict", "30")
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if state != domain.StateNeedsInfo {
		t.Fatalf("state = %s, want NEEDS_INFO", state)
	}
	if trk.checkCalls != 0 {
		t.Fatalf("GetPullRequestChecks calls = %d, want 0 (conflict short-circuits checks)", trk.checkCalls)
	}

	issue, err := store.GetIssue(context.Background(), "exec-conflict", "30")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.State != domain.StateNeedsInfo {
		t.Fatalf("persisted state = %s, want NEEDS_INFO", issue.State)
	}

	runs, err := store.CIRunsByIssue(context.Background(), "exec-conflict", "30")
	if err != nil {
		t.Fatalf("CIRunsByIssue: %v", err)
	}
	if len(runs) != 1 || runs[0].Kind != storage.CIRunKindConflict || runs[0].Status != storage.CIRunStatusFailed {
		t.Fatalf("runs = %+v, want one FAILED conflict run", runs)
	}

	if len(needsInfo.labels["30"]) != 1 || needsInfo.labels["30"][0] != "forge-blocked" {
		t.Fatalf("labels = %v, want [forge-blocked]", needsInfo.labels["30"])
	}
	if len(needsInfo.comments) != 1 {
		t.Fatalf("comments = %d, want 1", len(needsInfo.comments))
	}

	checkpoint, err := store.GetNeedsInfoCheckpoint(context.Background(), "exec-conflict", "30")
	if err != nil {
		t.Fatalf("GetNeedsInfoCheckpoint: %v", err)
	}
	if !checkpoint.CommentPosted || checkpoint.CommentAuthor != "forge-bot" {
		t.Fatalf("checkpoint = %+v, want CommentPosted with forge-bot author", checkpoint)
	}
}

func TestWait_ActionableReviewChangesRequested_TransitionsToCIFailedAsReviewKind(t *testing.T) {
	store := openTestStore(t)
	seedIssueWithPR(t, store, "exec-review", "31")

	trk := &stubTrackerWithReviews{
		stubTracker: stubTracker{mergeRequirements: tracker.MergeRequirements{RequiredChecks: []string{"build"}}},
		reviews: []tracker.PullRequestReview{
			{ID: 1, Author: "alice", State: tracker.ReviewApproved, Body: "lgtm"},
			{ID: 2, Author: "bob", State: tracker.ReviewChangesRequested, Body: "please rename this function"},
		},
	}
	supervisor := ci.New(store, trk, config.Default(), "main")
	supervisor.Now = func() time.Time { return time.Date(2026, 8, 28, 12, 7, 0, 0, time.UTC) }

	state, err := supervisor.Wait(context.Background(), "exec-review", "31")
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if state != domain.StateCIFailed {
		t.Fatalf("state = %s, want CI_FAILED", state)
	}
	if trk.checkCalls != 0 {
		t.Fatalf("GetPullRequestChecks calls = %d, want 0 (actionable review short-circuits checks)", trk.checkCalls)
	}

	runs, err := store.CIRunsByIssue(context.Background(), "exec-review", "31")
	if err != nil {
		t.Fatalf("CIRunsByIssue: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1", len(runs))
	}
	run := runs[0]
	if run.Kind != storage.CIRunKindReview || run.Status != storage.CIRunStatusFailed {
		t.Fatalf("run = %+v, want FAILED review run", run)
	}
	if run.CheckName != "bob" || run.Details != "please rename this function" {
		t.Fatalf("run CheckName/Details = %q/%q, want bob/\"please rename this function\"", run.CheckName, run.Details)
	}
}

func TestWait_AmbiguousChangesRequestedReviews_RoutesToNeedsInfo(t *testing.T) {
	store := openTestStore(t)
	seedIssueWithPR(t, store, "exec-ambiguous", "32")

	trk := &stubTrackerWithReviews{
		stubTracker: stubTracker{mergeRequirements: tracker.MergeRequirements{RequiredChecks: []string{"build"}}},
		reviews: []tracker.PullRequestReview{
			{ID: 1, Author: "alice", State: tracker.ReviewChangesRequested, Body: "use approach A"},
			{ID: 2, Author: "bob", State: tracker.ReviewChangesRequested, Body: "use approach B instead"},
		},
	}
	supervisor := ci.New(store, trk, config.Default(), "main")
	supervisor.Now = func() time.Time { return time.Date(2026, 8, 28, 12, 8, 0, 0, time.UTC) }

	state, err := supervisor.Wait(context.Background(), "exec-ambiguous", "32")
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if state != domain.StateNeedsInfo {
		t.Fatalf("state = %s, want NEEDS_INFO", state)
	}

	runs, err := store.CIRunsByIssue(context.Background(), "exec-ambiguous", "32")
	if err != nil {
		t.Fatalf("CIRunsByIssue: %v", err)
	}
	if len(runs) != 1 || runs[0].Kind != storage.CIRunKindReview {
		t.Fatalf("runs = %+v, want one review-kind run", runs)
	}
}

func TestWait_NonActionableReviewFeedback_FallsThroughToChecks(t *testing.T) {
	store := openTestStore(t)
	seedIssueWithPR(t, store, "exec-nonactionable", "33")

	trk := &stubTrackerWithReviews{
		stubTracker: stubTracker{
			mergeRequirements: tracker.MergeRequirements{RequiredChecks: []string{"build"}},
			checkResponses: [][]tracker.PullRequestCheck{
				{{Name: "build", State: tracker.CheckSuccess}},
			},
		},
		reviews: []tracker.PullRequestReview{
			{ID: 1, Author: "alice", State: tracker.ReviewApproved, Body: "lgtm"},
			{ID: 2, Author: "bob", State: tracker.ReviewChangesRequested, Body: ""},
		},
	}
	supervisor := ci.New(store, trk, config.Default(), "main")
	supervisor.Now = func() time.Time { return time.Date(2026, 8, 28, 12, 9, 0, 0, time.UTC) }

	state, err := supervisor.Wait(context.Background(), "exec-nonactionable", "33")
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if state != domain.StateDone {
		t.Fatalf("state = %s, want DONE (non-actionable review feedback must not block checks)", state)
	}
	if trk.checkCalls != 1 {
		t.Fatalf("GetPullRequestChecks calls = %d, want 1", trk.checkCalls)
	}
}
