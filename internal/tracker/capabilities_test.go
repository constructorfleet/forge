package tracker_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/tracker"
)

var _ tracker.Tracker = (*issueOnlyTracker)(nil)
var _ tracker.LegacyProvider = (*combinedTracker)(nil)
var _ tracker.Tracker = (*tracker.FakeTracker)(nil)
var _ tracker.SCM = (*scmOnly)(nil)
var _ tracker.CI = (*ciOnly)(nil)
var _ tracker.ReviewGetter = (*reviewOnly)(nil)

type issueOnlyTracker struct{}

func (issueOnlyTracker) GetIssue(context.Context, string) (domain.Issue, error) {
	return domain.Issue{}, nil
}
func (issueOnlyTracker) GetIssues(context.Context, []string) ([]domain.Issue, error) {
	return nil, nil
}
func (issueOnlyTracker) GetComments(context.Context, string) ([]tracker.Comment, error) {
	return nil, nil
}
func (issueOnlyTracker) AddComment(context.Context, string, string) (tracker.Comment, error) {
	return tracker.Comment{}, nil
}
func (issueOnlyTracker) AddLabel(context.Context, string, string) error { return nil }
func (issueOnlyTracker) RemoveLabel(context.Context, string, string) error {
	return nil
}
func (issueOnlyTracker) CreateIssue(context.Context, tracker.IssueRequest) (tracker.CreatedIssue, error) {
	return tracker.CreatedIssue{}, nil
}
func (issueOnlyTracker) UpdateIssue(context.Context, string, tracker.UpdateIssueRequest) error {
	return nil
}
func (issueOnlyTracker) Capabilities() tracker.Capabilities { return tracker.Capabilities{} }

type combinedTracker struct {
	issueOnlyTracker
}

func (combinedTracker) GetMergeRequirements(context.Context, string) (tracker.MergeRequirements, error) {
	return tracker.MergeRequirements{}, nil
}
func (combinedTracker) GetPullRequestChecks(context.Context, int) ([]tracker.PullRequestCheck, error) {
	return nil, nil
}
func (combinedTracker) CreatePullRequest(context.Context, tracker.PullRequestRequest) (tracker.PullRequest, error) {
	return tracker.PullRequest{}, nil
}

type scmOnly struct{}

func (scmOnly) CreateChangeRequest(context.Context, tracker.ChangeRequestRequest) (tracker.ChangeRequest, error) {
	return tracker.ChangeRequest{}, nil
}
func (scmOnly) GetChangeRequestMergeStatus(context.Context, tracker.ChangeRequestRef) (tracker.ChangeRequestMergeStatus, error) {
	return tracker.ChangeRequestMergeStatus{}, nil
}

type ciOnly struct{}

func (ciOnly) GetMergeRequirements(context.Context, string) (tracker.MergeRequirements, error) {
	return tracker.MergeRequirements{Requirements: []tracker.MergeRequirement{{CheckName: "build"}}}, nil
}
func (ciOnly) GetChecks(context.Context, tracker.ChangeRequestRef) ([]tracker.Check, error) {
	return nil, nil
}

type reviewOnly struct{}

func (reviewOnly) GetReviews(context.Context, tracker.ChangeRequestRef) ([]tracker.Review, error) {
	return nil, nil
}

func TestMergeBlockerCarriesReasonSourceAndRawProviderDetail(t *testing.T) {
	blocker := tracker.MergeBlocker{
		Reason:    tracker.ChecksFailing,
		Source:    tracker.CapabilityCI,
		RawDetail: "GitHub Actions reported build failed",
	}

	if blocker.Reason != tracker.ChecksFailing {
		t.Fatalf("Reason = %q, want %q", blocker.Reason, tracker.ChecksFailing)
	}
	if blocker.Source != tracker.CapabilityCI {
		t.Fatalf("Source = %q, want %q", blocker.Source, tracker.CapabilityCI)
	}
	if blocker.RawDetail != "GitHub Actions reported build failed" {
		t.Fatalf("RawDetail = %q", blocker.RawDetail)
	}
}

func TestTrackerIsIssueDomainOnlyAndLegacyProviderPreservesCombinedContract(t *testing.T) {
	trackerType := reflect.TypeOf((*tracker.Tracker)(nil)).Elem()
	for _, legacyMethod := range []string{"CreatePullRequest", "GetPullRequestChecks", "GetMergeRequirements"} {
		if _, ok := trackerType.MethodByName(legacyMethod); ok {
			t.Fatalf("tracker.Tracker must not expose legacy change-request method %s", legacyMethod)
		}
	}

	legacyType := reflect.TypeOf((*tracker.LegacyProvider)(nil)).Elem()
	for _, method := range []string{"GetIssue", "CreateIssue", "CreatePullRequest", "GetPullRequestChecks", "GetMergeRequirements"} {
		if _, ok := legacyType.MethodByName(method); !ok {
			t.Fatalf("tracker.LegacyProvider missing method %s", method)
		}
	}
}

func TestNeutralChangeRequestValueTypes(t *testing.T) {
	ref := tracker.ChangeRequestRef{Provider: "github", Number: 293}
	change := tracker.ChangeRequest{Ref: ref, URL: "https://example.invalid/pr/293"}
	check := tracker.Check{Name: "build", State: tracker.CheckPending, Details: "queued"}
	requirement := tracker.MergeRequirement{CheckName: "build"}
	review := tracker.Review{Author: "alice", State: tracker.ReviewApproved, Body: "ship it"}
	approval := tracker.Approval{Author: "alice", RawDetail: "APPROVED"}
	eligibility := tracker.MergeEligibility{
		Mergeable: false,
		Blockers:  []tracker.MergeBlocker{{Reason: tracker.ChecksPending, Source: tracker.CapabilityCI}},
	}

	if change.Ref != ref {
		t.Fatalf("ChangeRequest.Ref = %+v, want %+v", change.Ref, ref)
	}
	if check.Name != requirement.CheckName {
		t.Fatalf("check/requirement mismatch: %+v %+v", check, requirement)
	}
	if review.Author != approval.Author {
		t.Fatalf("review/approval author mismatch: %+v %+v", review, approval)
	}
	if eligibility.Mergeable || len(eligibility.Blockers) != 1 {
		t.Fatalf("eligibility = %+v", eligibility)
	}
}

func TestNeutralCapabilityTypesAreDistinctFromLegacyPullRequestTypes(t *testing.T) {
	if reflect.TypeOf(tracker.ChangeRequestRequest{}) == reflect.TypeOf(tracker.PullRequestRequest{}) {
		t.Fatal("ChangeRequestRequest must be a distinct neutral type, not an alias of PullRequestRequest")
	}
	if reflect.TypeOf(tracker.Check{}) == reflect.TypeOf(tracker.PullRequestCheck{}) {
		t.Fatal("Check must be a distinct neutral type, not an alias of PullRequestCheck")
	}
	if reflect.TypeOf(tracker.Review{}) == reflect.TypeOf(tracker.PullRequestReview{}) {
		t.Fatal("Review must be a distinct neutral type, not an alias of PullRequestReview")
	}
	if _, ok := reflect.TypeOf(tracker.Review{}).FieldByName("ID"); ok {
		t.Fatal("Review must not expose provider-native review IDs")
	}
}
