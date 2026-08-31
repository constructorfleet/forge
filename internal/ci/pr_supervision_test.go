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
	conflicted      bool
	conflictedUntil int
	mergedAfter     int
	mergeCalls      int
}

func (s *stubTrackerWithMergeStatus) GetPullRequestMergeStatus(context.Context, int) (tracker.PullRequestMergeStatus, error) {
	s.mergeCalls++
	conflicted := s.conflicted
	if s.conflictedUntil > 0 {
		conflicted = s.mergeCalls <= s.conflictedUntil
	}
	return tracker.PullRequestMergeStatus{
		Conflicted: conflicted,
		Merged:     s.mergedAfter > 0 && s.mergeCalls >= s.mergedAfter,
	}, nil
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

type stubConflictResolver struct {
	calls    int
	requests []ci.ConflictResolutionRequest
	result   ci.ConflictResolutionResult
	err      error
}

func (s *stubConflictResolver) ResolveMergeConflict(_ context.Context, req ci.ConflictResolutionRequest) (ci.ConflictResolutionResult, error) {
	s.calls++
	s.requests = append(s.requests, req)
	return s.result, s.err
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

func TestWait_MergeConflict_ResolverSuccessRecordsRepairAndContinuesToChecks(t *testing.T) {
	store := openTestStore(t)
	seedIssueWithPR(t, store, "exec-conflict-resolved", "32")

	trk := &stubTrackerWithMergeStatus{
		stubTracker: stubTracker{
			mergeRequirements: tracker.MergeRequirements{RequiredChecks: []string{"build"}},
			checkResponses: [][]tracker.PullRequestCheck{
				{{Name: "build", State: tracker.CheckSuccess}},
			},
		},
		conflictedUntil: 1,
		mergedAfter:     2,
	}

	supervisor := ci.New(store, trk, config.Default(), "main")
	sleep := &sleepRecorder{}
	supervisor.Sleep = sleep.Sleep
	supervisor.Now = func() time.Time { return time.Date(2026, 8, 28, 12, 7, 0, 0, time.UTC) }
	resolver := &stubConflictResolver{
		result: ci.ConflictResolutionResult{Resolved: true, Details: "rebased branch onto main and quality gates passed"},
	}
	supervisor.ConflictResolver = resolver

	state, err := supervisor.Wait(context.Background(), "exec-conflict-resolved", "32")
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if state != domain.StateDone {
		t.Fatalf("state = %s, want DONE", state)
	}
	if resolver.calls != 1 {
		t.Fatalf("ResolveMergeConflict calls = %d, want 1", resolver.calls)
	}
	if got := resolver.requests[0]; got.ExecutionID != "exec-conflict-resolved" || got.IssueID != "32" || got.PullRequestNumber != 23 || got.BaseBranch != "main" || got.PullRequestHeadSHA != "abc123" {
		t.Fatalf("resolver request = %+v, want execution/issue/pr/base/head populated from persisted PR", got)
	}
	if trk.checkCalls != 1 {
		t.Fatalf("GetPullRequestChecks calls = %d, want 1 after conflict repair", trk.checkCalls)
	}

	runs, err := store.CIRunsByIssue(context.Background(), "exec-conflict-resolved", "32")
	if err != nil {
		t.Fatalf("CIRunsByIssue: %v", err)
	}
	var conflictRuns int
	for _, run := range runs {
		if run.Kind == storage.CIRunKindConflict {
			conflictRuns++
			if run.Status != storage.CIRunStatusPassed {
				t.Fatalf("conflict repair run status = %s, want PASSED", run.Status)
			}
			if run.Details != "rebased branch onto main and quality gates passed" {
				t.Fatalf("conflict repair details = %q, want resolver details", run.Details)
			}
		}
	}
	if conflictRuns != 1 {
		t.Fatalf("conflict repair runs = %d, want 1", conflictRuns)
	}
}

func TestWait_MergeConflict_ResolverRefusalRoutesToNeedsInfoWithDetails(t *testing.T) {
	store := openTestStore(t)
	seedIssueWithPR(t, store, "exec-conflict-refused", "33")

	trk := &stubTrackerWithMergeStatus{
		stubTracker: stubTracker{mergeRequirements: tracker.MergeRequirements{RequiredChecks: []string{"build"}}},
		conflicted:  true,
	}

	cfg := config.Default()
	cfg.Blocked.Label = "forge-blocked"
	cfg.Blocked.Comment = true

	supervisor := ci.New(store, trk, cfg, "main")
	supervisor.Now = func() time.Time { return time.Date(2026, 8, 28, 12, 8, 0, 0, time.UTC) }
	supervisor.NeedsInfoTracker = newStubNeedsInfoTracker()
	supervisor.ConflictResolver = &stubConflictResolver{
		result: ci.ConflictResolutionResult{Resolved: false, Details: "automatic conflict replay refused: README.md still has unresolved hunks"},
	}

	state, err := supervisor.Wait(context.Background(), "exec-conflict-refused", "33")
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if state != domain.StateNeedsInfo {
		t.Fatalf("state = %s, want NEEDS_INFO", state)
	}
	if trk.checkCalls != 0 {
		t.Fatalf("GetPullRequestChecks calls = %d, want 0 after resolver refusal", trk.checkCalls)
	}

	runs, err := store.CIRunsByIssue(context.Background(), "exec-conflict-refused", "33")
	if err != nil {
		t.Fatalf("CIRunsByIssue: %v", err)
	}
	if len(runs) != 1 || runs[0].Kind != storage.CIRunKindConflict || runs[0].Status != storage.CIRunStatusFailed {
		t.Fatalf("runs = %+v, want one FAILED conflict run", runs)
	}
	if runs[0].Details != "automatic conflict replay refused: README.md still has unresolved hunks" {
		t.Fatalf("run details = %q, want resolver refusal details", runs[0].Details)
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
