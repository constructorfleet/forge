package ci_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/ci"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/needsinfo"
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

type stubConflictRestorer struct {
	calls       int
	paths       []string
	branch      []string
	restoreSHA  []string
	expectedSHA []string
	resetPath   []string
	resetSHA    []string
	err         error
	readyErr    error
}

func (s *stubConflictRestorer) EnsureWorkspaceReady(context.Context, string) error {
	return s.readyErr
}

func (s *stubConflictRestorer) ForcePushCommitWithLease(_ context.Context, path, branch, commitSHA, expectedRemoteSHA string) error {
	s.calls++
	s.paths = append(s.paths, path)
	s.branch = append(s.branch, branch)
	s.restoreSHA = append(s.restoreSHA, commitSHA)
	s.expectedSHA = append(s.expectedSHA, expectedRemoteSHA)
	return s.err
}

func (s *stubConflictRestorer) Reset(_ context.Context, path, commitSHA string) error {
	s.resetPath = append(s.resetPath, path)
	s.resetSHA = append(s.resetSHA, commitSHA)
	return nil
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
	if !strings.Contains(needsInfo.comments[0], needsinfo.CommentMarker("exec-conflict", "30")) {
		t.Fatalf("comment body missing needs-info marker: %s", needsInfo.comments[0])
	}

	checkpoint, err := store.GetNeedsInfoCheckpoint(context.Background(), "exec-conflict", "30")
	if err != nil {
		t.Fatalf("GetNeedsInfoCheckpoint: %v", err)
	}
	if !checkpoint.CommentPosted || checkpoint.CommentAuthor != "forge-bot" {
		t.Fatalf("checkpoint = %+v, want CommentPosted with forge-bot author", checkpoint)
	}
}

func TestWait_PublishedConflictCandidateFailedCheck_RestoresAndRoutesToNeedsInfo(t *testing.T) {
	store := openTestStore(t)
	seedIssueWithPR(t, store, "exec-conflict-ci-failed", "36")
	seedWorkspace(t, store, "exec-conflict-ci-failed", "36", "/tmp/ws-36", "forge/exec-conflict-ci-failed/36")
	now := time.Date(2026, 8, 31, 11, 0, 0, 0, time.UTC)
	if err := store.RecordConflictResolutionAttempt(context.Background(), storage.ConflictResolutionAttempt{
		ExecutionID:  "exec-conflict-ci-failed",
		IssueID:      "36",
		PRNumber:     23,
		Branch:       "forge/exec-conflict-ci-failed/36",
		OriginalSHA:  "abc123",
		CandidateSHA: "def456",
		Status:       storage.ConflictResolutionStatusPublished,
		Details:      "published candidate",
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatalf("RecordConflictResolutionAttempt: %v", err)
	}

	trk := &stubTrackerWithMergeStatus{
		stubTracker: stubTracker{
			mergeRequirements: tracker.MergeRequirements{RequiredChecks: []string{"build"}},
			checkResponses: [][]tracker.PullRequestCheck{
				{{Name: "build", State: tracker.CheckFailure, Details: "unit tests failed"}},
			},
		},
	}

	cfg := config.Default()
	cfg.Blocked.Label = "forge-blocked"
	supervisor := ci.New(store, trk, cfg, "main")
	restorer := &stubConflictRestorer{}
	supervisor.ConflictRestorer = restorer
	supervisor.NeedsInfoTracker = newStubNeedsInfoTracker()
	supervisor.Now = func() time.Time { return time.Date(2026, 8, 31, 11, 5, 0, 0, time.UTC) }

	state, err := supervisor.Wait(context.Background(), "exec-conflict-ci-failed", "36")
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if state != domain.StateNeedsInfo {
		t.Fatalf("state = %s, want NEEDS_INFO", state)
	}
	if restorer.calls != 1 {
		t.Fatalf("ForcePushCommitWithLease calls = %d, want 1", restorer.calls)
	}
	if restorer.paths[0] != "/tmp/ws-36" || restorer.branch[0] != "forge/exec-conflict-ci-failed/36" || restorer.restoreSHA[0] != "abc123" || restorer.expectedSHA[0] != "def456" {
		t.Fatalf("restore call = path %q branch %q restore %q expected %q, want live workspace/original/candidate lease", restorer.paths[0], restorer.branch[0], restorer.restoreSHA[0], restorer.expectedSHA[0])
	}
	if len(restorer.resetSHA) != 1 || restorer.resetSHA[0] != "abc123" {
		t.Fatalf("Reset calls = %v, want abc123", restorer.resetSHA)
	}
	if _, err := store.ActiveConflictResolutionAttempt(context.Background(), "exec-conflict-ci-failed", "36"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("ActiveConflictResolutionAttempt err = %v, want ErrNotFound after restore", err)
	}
	issue, err := store.GetIssue(context.Background(), "exec-conflict-ci-failed", "36")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.State != domain.StateNeedsInfo {
		t.Fatalf("persisted state = %s, want NEEDS_INFO", issue.State)
	}
}

func TestWait_PublishedConflictCandidateActionableReview_RestoresAndRoutesToNeedsInfo(t *testing.T) {
	store := openTestStore(t)
	seedIssueWithPR(t, store, "exec-conflict-review-failed", "37")
	seedWorkspace(t, store, "exec-conflict-review-failed", "37", "/tmp/ws-37", "forge/exec-conflict-review-failed/37")
	now := time.Date(2026, 8, 31, 11, 10, 0, 0, time.UTC)
	if err := store.RecordConflictResolutionAttempt(context.Background(), storage.ConflictResolutionAttempt{
		ExecutionID:  "exec-conflict-review-failed",
		IssueID:      "37",
		PRNumber:     23,
		Branch:       "forge/exec-conflict-review-failed/37",
		OriginalSHA:  "abc123",
		CandidateSHA: "def456",
		Status:       storage.ConflictResolutionStatusPublished,
		Details:      "published candidate",
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatalf("RecordConflictResolutionAttempt: %v", err)
	}

	trk := &stubTrackerWithReviews{
		stubTracker: stubTracker{mergeRequirements: tracker.MergeRequirements{RequiredChecks: []string{"build"}}},
		reviews: []tracker.PullRequestReview{
			{ID: 1, Author: "bob", State: tracker.ReviewChangesRequested, Body: "this replay broke the edge case"},
		},
	}
	supervisor := ci.New(store, trk, config.Default(), "main")
	restorer := &stubConflictRestorer{}
	supervisor.ConflictRestorer = restorer
	supervisor.NeedsInfoTracker = newStubNeedsInfoTracker()
	supervisor.Now = func() time.Time { return time.Date(2026, 8, 31, 11, 15, 0, 0, time.UTC) }

	state, err := supervisor.Wait(context.Background(), "exec-conflict-review-failed", "37")
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if state != domain.StateNeedsInfo {
		t.Fatalf("state = %s, want NEEDS_INFO", state)
	}
	if restorer.calls != 1 || restorer.restoreSHA[0] != "abc123" || restorer.expectedSHA[0] != "def456" {
		t.Fatalf("restore calls = %+v restore %v expected %v, want original abc123 with candidate lease def456", restorer.calls, restorer.restoreSHA, restorer.expectedSHA)
	}
	if _, err := store.ActiveConflictResolutionAttempt(context.Background(), "exec-conflict-review-failed", "37"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("ActiveConflictResolutionAttempt err = %v, want ErrNotFound after restore", err)
	}
	runs, err := store.CIRunsByIssue(context.Background(), "exec-conflict-review-failed", "37")
	if err != nil {
		t.Fatalf("CIRunsByIssue: %v", err)
	}
	if len(runs) != 1 || runs[0].Kind != storage.CIRunKindReview || runs[0].Status != storage.CIRunStatusFailed {
		t.Fatalf("runs = %+v, want one failed review run before NEEDS_INFO", runs)
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
		// Wait fetches merge status once per iteration (shared by
		// pollConflict/pollStale/evaluateMergeEligibility), so the resolver's
		// repair, the checks poll, and the merge-eligibility decision all
		// observe this same call-1 status within one iteration.
		conflictedUntil: 1,
		mergedAfter:     1,
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

func TestWait_MergeConflict_MissingRecordedHeadRoutesToNeedsInfoWithoutResolver(t *testing.T) {
	store := openTestStore(t)
	seedIssueWithPR(t, store, "exec-conflict-no-head", "38")
	prs, err := store.PullRequestsByIssue(context.Background(), "exec-conflict-no-head", "38")
	if err != nil {
		t.Fatalf("PullRequestsByIssue: %v", err)
	}
	prs[0].CommitSHA = ""
	if err := store.RecordPullRequest(context.Background(), prs[0]); err != nil {
		t.Fatalf("RecordPullRequest: %v", err)
	}

	trk := &stubTrackerWithMergeStatus{
		stubTracker: stubTracker{mergeRequirements: tracker.MergeRequirements{RequiredChecks: []string{"build"}}},
		conflicted:  true,
	}
	supervisor := ci.New(store, trk, config.Default(), "main")
	resolver := &stubConflictResolver{result: ci.ConflictResolutionResult{Resolved: true}}
	supervisor.ConflictResolver = resolver
	supervisor.NeedsInfoTracker = newStubNeedsInfoTracker()
	supervisor.Now = func() time.Time { return time.Date(2026, 8, 31, 11, 20, 0, 0, time.UTC) }

	state, err := supervisor.Wait(context.Background(), "exec-conflict-no-head", "38")
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if state != domain.StateNeedsInfo {
		t.Fatalf("state = %s, want NEEDS_INFO", state)
	}
	if resolver.calls != 0 {
		t.Fatalf("ResolveMergeConflict calls = %d, want 0 without recorded PR head", resolver.calls)
	}
	runs, err := store.CIRunsByIssue(context.Background(), "exec-conflict-no-head", "38")
	if err != nil {
		t.Fatalf("CIRunsByIssue: %v", err)
	}
	if len(runs) != 1 || !strings.Contains(runs[0].Details, "recorded pull request head SHA is empty") {
		t.Fatalf("runs = %+v, want missing-head conflict failure", runs)
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
