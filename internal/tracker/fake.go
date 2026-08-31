package tracker

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/Teagan42/forge/internal/domain"
)

// var declaration ensures FakeTracker satisfies Tracker at compile time.
var _ Tracker = (*FakeTracker)(nil)

// FakeTracker is an in-memory Tracker and LegacyProvider implementation for
// tests: it never hits a real tracker API. It includes the old combined
// pull-request and CI methods so existing tests do not need an httptest-backed
// github.Client double.
type FakeTracker struct {
	mu sync.Mutex

	issues             map[string]domain.Issue
	comments           map[string][]Comment
	labels             map[string][]string
	mergeRequirements  map[string]MergeRequirements
	pullRequestChecks  map[int][]PullRequestCheck
	pullRequestsByHead map[string]PullRequest
	capabilities       Capabilities

	nextIssueID int
	nextPRNum   int
}

// NewFakeTracker returns an empty FakeTracker with no programmed Issues,
// comments, or labels.
func NewFakeTracker() *FakeTracker {
	return &FakeTracker{
		issues:             map[string]domain.Issue{},
		comments:           map[string][]Comment{},
		labels:             map[string][]string{},
		mergeRequirements:  map[string]MergeRequirements{},
		pullRequestChecks:  map[int][]PullRequestCheck{},
		pullRequestsByHead: map[string]PullRequest{},
		nextIssueID:        1,
		nextPRNum:          1,
	}
}

// AddIssue seeds issue into the fake, keyed by issue.ID, as if it already
// existed on the tracker. It does not affect the CreateIssue ID sequence.
func (f *FakeTracker) AddIssue(issue domain.Issue) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.issues[issue.ID] = issue
}

// SetCapabilities configures the value Capabilities returns.
func (f *FakeTracker) SetCapabilities(c Capabilities) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.capabilities = c
}

// SetMergeRequirements configures the MergeRequirements GetMergeRequirements
// returns for branch.
func (f *FakeTracker) SetMergeRequirements(branch string, reqs MergeRequirements) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mergeRequirements[branch] = reqs
}

// SetPullRequestChecks configures the checks GetPullRequestChecks returns
// for pull request number.
func (f *FakeTracker) SetPullRequestChecks(number int, checks []PullRequestCheck) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pullRequestChecks[number] = checks
}

// GetIssue returns the Issue seeded (via AddIssue or CreateIssue) under id.
func (f *FakeTracker) GetIssue(_ context.Context, id string) (domain.Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	issue, ok := f.issues[id]
	if !ok {
		return domain.Issue{}, fmt.Errorf("tracker: fake: no issue %q", id)
	}
	return issue, nil
}

// GetIssues returns the Issues seeded under each of ids, in order.
func (f *FakeTracker) GetIssues(ctx context.Context, ids []string) ([]domain.Issue, error) {
	issues := make([]domain.Issue, 0, len(ids))
	for _, id := range ids {
		issue, err := f.GetIssue(ctx, id)
		if err != nil {
			return nil, err
		}
		issues = append(issues, issue)
	}
	return issues, nil
}

// CreateIssue creates a new Issue with an auto-assigned ID and stores it so
// a subsequent GetIssue can fetch it back.
func (f *FakeTracker) CreateIssue(_ context.Context, req IssueRequest) (CreatedIssue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	id := strconv.Itoa(f.nextIssueID)
	f.nextIssueID++

	f.issues[id] = domain.Issue{ID: id, Title: req.Title, Body: req.Body}
	return CreatedIssue{ID: id, URL: fmt.Sprintf("https://fake.tracker/issues/%s", id)}, nil
}

// UpdateIssue replaces id's stored body.
func (f *FakeTracker) UpdateIssue(_ context.Context, id string, req UpdateIssueRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	issue, ok := f.issues[id]
	if !ok {
		return fmt.Errorf("tracker: fake: no issue %q", id)
	}
	issue.Body = req.Body
	f.issues[id] = issue
	return nil
}

// GetComments returns the comments posted on id, oldest first.
func (f *FakeTracker) GetComments(_ context.Context, id string) ([]Comment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Comment, len(f.comments[id]))
	copy(out, f.comments[id])
	return out, nil
}

// AddComment appends a comment to id and returns it normalized, mirroring
// the real Tracker.AddComment contract (author identity + CreatedAt come
// from the tracker, not the caller).
func (f *FakeTracker) AddComment(_ context.Context, id string, body string) (Comment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := Comment{Author: "forge-bot", Body: body, CreatedAt: time.Now()}
	f.comments[id] = append(f.comments[id], c)
	return c, nil
}

// AddLabel idempotently ensures label is set on id.
func (f *FakeTracker) AddLabel(_ context.Context, id string, label string) error {
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

// RemoveLabel idempotently ensures label is not set on id.
func (f *FakeTracker) RemoveLabel(_ context.Context, id string, label string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	kept := f.labels[id][:0]
	for _, l := range f.labels[id] {
		if l != label {
			kept = append(kept, l)
		}
	}
	f.labels[id] = kept
	return nil
}

// Labels returns the labels currently set on id.
func (f *FakeTracker) Labels(id string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.labels[id]...)
}

// GetMergeRequirements returns the MergeRequirements configured via
// SetMergeRequirements for branch, or a zero value if none was set.
func (f *FakeTracker) GetMergeRequirements(_ context.Context, branch string) (MergeRequirements, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mergeRequirements[branch], nil
}

// GetPullRequestChecks returns the checks configured via
// SetPullRequestChecks for number, or nil if none were set.
func (f *FakeTracker) GetPullRequestChecks(_ context.Context, number int) ([]PullRequestCheck, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]PullRequestCheck(nil), f.pullRequestChecks[number]...), nil
}

// CreatePullRequest idempotently creates a pull request from req.Head into
// req.Base: a repeated call with the same Head recovers the
// previously-created PullRequest rather than duplicating it, mirroring the
// real Tracker.CreatePullRequest contract.
func (f *FakeTracker) CreatePullRequest(_ context.Context, req PullRequestRequest) (PullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if existing, ok := f.pullRequestsByHead[req.Head]; ok {
		return existing, nil
	}
	pr := PullRequest{Number: f.nextPRNum, URL: fmt.Sprintf("https://fake.tracker/pulls/%d", f.nextPRNum)}
	f.nextPRNum++
	f.pullRequestsByHead[req.Head] = pr
	return pr, nil
}

// Capabilities returns the value configured via SetCapabilities, or a zero
// Capabilities (no optional behaviors) if none was set.
func (f *FakeTracker) Capabilities() Capabilities {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.capabilities
}
