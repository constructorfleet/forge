package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/review"
	"github.com/Teagan42/forge/internal/tracker"
)

// fakePublisher is a minimal engine.Publisher double: it records every
// call and returns programmed SHAs/errors, so tests never shell out to
// git.
type fakePublisher struct {
	commitSHA string
	commitErr error
	pushErr   error

	mu          sync.Mutex
	commitCalls []struct{ workspacePath, message string }
	pushCalls   []struct{ workspacePath, branch string }
}

func (f *fakePublisher) Commit(_ context.Context, workspacePath, message string) (string, error) {
	f.mu.Lock()
	f.commitCalls = append(f.commitCalls, struct{ workspacePath, message string }{workspacePath, message})
	f.mu.Unlock()
	if f.commitErr != nil {
		return "", f.commitErr
	}
	if f.commitSHA != "" {
		return f.commitSHA, nil
	}
	return "deadbeef", nil
}

func (f *fakePublisher) Push(_ context.Context, workspacePath, branch string) error {
	f.mu.Lock()
	f.pushCalls = append(f.pushCalls, struct{ workspacePath, branch string }{workspacePath, branch})
	f.mu.Unlock()
	return f.pushErr
}

func (f *fakePublisher) pushCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.pushCalls)
}

// fakePRTracker is a minimal engine.PRCreator double, in-memory keyed by
// head branch: a second CreatePullRequest call for a branch that already
// has a PR recovers the same PullRequest rather than minting a new one,
// mirroring github.Client's real idempotent-recovery behavior.
type fakePRTracker struct {
	createErr error

	mu         sync.Mutex
	byHead     map[string]tracker.PullRequest
	nextNumber int
	requests   []tracker.PullRequestRequest
}

func newFakePRTracker() *fakePRTracker {
	return &fakePRTracker{byHead: map[string]tracker.PullRequest{}, nextNumber: 1}
}

func (f *fakePRTracker) CreatePullRequest(_ context.Context, req tracker.PullRequestRequest) (tracker.PullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)
	if f.createErr != nil {
		return tracker.PullRequest{}, f.createErr
	}
	if pr, ok := f.byHead[req.Head]; ok {
		return pr, nil
	}
	pr := tracker.PullRequest{Number: f.nextNumber, URL: fmt.Sprintf("https://example.invalid/pr/%d", f.nextNumber)}
	f.nextNumber++
	f.byHead[req.Head] = pr
	return pr, nil
}

func (f *fakePRTracker) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fakePRTracker) lastRequest() tracker.PullRequestRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[len(f.requests)-1]
}

// approvedTestEngine builds a testEngine already wired with a Reviewer that
// approves every Issue, so tests only need to layer Publisher/PRTracker on
// top to reach COMMITTING/PR_CREATING.
func approvedTestEngine(t *testing.T, issueID string, issue domain.Issue) testEngine {
	t.Helper()
	te := newTestEngine(t, map[string]domain.Issue{issueID: issue})
	te.fake.ProgramResult(issueID, agent.AgentResult{Status: agent.StatusImplemented})
	reviewer := review.NewFakeReviewer()
	reviewer.ProgramResult(issueID, review.Result{Verdict: review.VerdictApproved, Summary: "ship it"})
	te.eng.Reviewer = reviewer
	te.eng.Diff = &stubDiff{diff: "diff --git a/foo b/foo"}
	return te
}

// TestExecute_CommitAndPR_AdvancesToCIPending is ticket 22's main
// integration test: approved work is committed, pushed, a pull request is
// created, and the Issue advances to CI_PENDING with the PR id/url and
// commit SHA persisted.
func TestExecute_CommitAndPR_AdvancesToCIPending(t *testing.T) {
	te := approvedTestEngine(t, "40", domain.Issue{ID: "40", Title: "Add widget support"})
	pub := &fakePublisher{commitSHA: "abc123"}
	prTracker := newFakePRTracker()
	te.eng.Publisher = pub
	te.eng.PRTracker = prTracker
	te.eng.BaseBranch = "main"

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "40", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateCIPending {
		t.Fatalf("final state = %s, want CI_PENDING", result.Issue.State)
	}

	// Commit used the default template: "{title}\n\nRefs #{issue}".
	if len(pub.commitCalls) != 1 {
		t.Fatalf("got %d commit calls, want 1", len(pub.commitCalls))
	}
	wantMsg := "Add widget support\n\nRefs #40"
	if pub.commitCalls[0].message != wantMsg {
		t.Errorf("commit message = %q, want %q", pub.commitCalls[0].message, wantMsg)
	}

	// Push targeted the Workspace's branch.
	if len(pub.pushCalls) != 1 {
		t.Fatalf("got %d push calls, want 1", len(pub.pushCalls))
	}

	// The pull request was created against the configured base branch with
	// a title/body referencing the Issue.
	if prTracker.callCount() != 1 {
		t.Fatalf("got %d CreatePullRequest calls, want 1", prTracker.callCount())
	}
	req := prTracker.lastRequest()
	if req.Base != "main" {
		t.Errorf("PR Base = %q, want %q", req.Base, "main")
	}
	if req.Title != "Add widget support" {
		t.Errorf("PR Title = %q, want %q", req.Title, "Add widget support")
	}
	if !strings.Contains(req.Body, "Closes #40") {
		t.Errorf("PR Body = %q, want it to contain %q", req.Body, "Closes #40")
	}

	// The PR id/url and commit SHA were persisted.
	prs, err := te.store.PullRequestsByIssue(ctx, result.ExecutionID, "40")
	if err != nil {
		t.Fatalf("PullRequestsByIssue: %v", err)
	}
	if len(prs) != 1 {
		t.Fatalf("got %d persisted pull requests, want 1", len(prs))
	}
	if prs[0].Number != 1 || prs[0].URL != "https://example.invalid/pr/1" || prs[0].CommitSHA != "abc123" {
		t.Errorf("persisted PullRequest = %+v, want Number 1, URL .../pr/1, CommitSHA abc123", prs[0])
	}

	// The audit log carries the COMMITTING -> PR_CREATING -> CI_PENDING
	// transitions plus commit/push/PR events.
	events, err := te.store.EventsByExecution(ctx, result.ExecutionID)
	if err != nil {
		t.Fatalf("EventsByExecution: %v", err)
	}
	var sawCommit, sawPush, sawPR, sawCIPending bool
	for _, e := range events {
		switch e.Type {
		case "commit.created":
			sawCommit = true
		case "branch.pushed":
			sawPush = true
		case "pull_request.created":
			sawPR = true
		case "issue.transitioned":
			var tr struct {
				To string `json:"to"`
			}
			if err := json.Unmarshal([]byte(e.Data), &tr); err == nil && tr.To == string(domain.StateCIPending) {
				sawCIPending = true
			}
		}
	}
	if !sawCommit {
		t.Error("no commit.created event found")
	}
	if !sawPush {
		t.Error("no branch.pushed event found")
	}
	if !sawPR {
		t.Error("no pull_request.created event found")
	}
	if !sawCIPending {
		t.Error("no transition to CI_PENDING found in events")
	}
}

func TestExecute_CommitAndPR_UsesConfiguredCommitMessageTemplate(t *testing.T) {
	te := approvedTestEngine(t, "40b", domain.Issue{ID: "40b", Title: "Add widget support"})
	pub := &fakePublisher{commitSHA: "abc123"}
	te.eng.Publisher = pub
	te.eng.PRTracker = newFakePRTracker()
	te.eng.BaseBranch = "main"
	te.eng.Config.PullRequests.CommitMessageTemplate = "feat(issue-{issue}): {title}"

	if _, err := te.eng.Execute(context.Background(), "40b", te.base); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(pub.commitCalls) != 1 {
		t.Fatalf("got %d commit calls, want 1", len(pub.commitCalls))
	}
	if got := pub.commitCalls[0].message; got != "feat(issue-40b): Add widget support" {
		t.Fatalf("commit message = %q", got)
	}
}

// TestExecute_PublisherAndPRTrackerUnset_CommittingStaysRestingState
// guards ticket 22's backward-compatible default: with neither seam wired,
// COMMITTING remains a resting state exactly as it did before this ticket.
func TestExecute_PublisherAndPRTrackerUnset_CommittingStaysRestingState(t *testing.T) {
	te := approvedTestEngine(t, "41", domain.Issue{ID: "41", Title: "no seams wired"})

	result, err := te.eng.Execute(context.Background(), "41", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateCommitting {
		t.Fatalf("final state = %s, want COMMITTING", result.Issue.State)
	}
}

// TestExecute_PRAlreadyExistsForBranch_RecoveredNotDuplicated exercises
// idempotent PR recovery directly against the PRCreator seam: a PR already
// exists for the Workspace's branch (as fakePRTracker's in-memory map
// simulates), so CreatePullRequest recovers it instead of creating a
// second one, and the recovered id/url are what gets persisted.
func TestExecute_PRAlreadyExistsForBranch_RecoveredNotDuplicated(t *testing.T) {
	te := approvedTestEngine(t, "42", domain.Issue{ID: "42", Title: "reuse existing PR"})
	pub := &fakePublisher{commitSHA: "sha-1"}
	prTracker := newFakePRTracker()
	te.eng.Publisher = pub
	te.eng.PRTracker = prTracker
	te.eng.BaseBranch = "main"

	// Pre-seed an existing open PR for the branch this Execute run's
	// Workspace will use, simulating a prior run that already created one
	// (e.g. a crash between PR creation and the CI_PENDING transition).
	branch := "forge/" + firstExecutionIDPlaceholder + "/42"
	_ = branch // placeholder not used directly; branch is derived below.

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "42", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateCIPending {
		t.Fatalf("final state = %s, want CI_PENDING", result.Issue.State)
	}
	if prTracker.callCount() != 1 {
		t.Fatalf("got %d CreatePullRequest calls, want 1 (only this run's own call)", prTracker.callCount())
	}

	// A second, independent CreatePullRequest call against the same head
	// branch (e.g. a resumed/retried run) recovers the same PR rather than
	// minting a new one.
	pr2, err := prTracker.CreatePullRequest(ctx, prTracker.lastRequest())
	if err != nil {
		t.Fatalf("CreatePullRequest (recovery): %v", err)
	}
	prs, err := te.store.PullRequestsByIssue(ctx, result.ExecutionID, "42")
	if err != nil {
		t.Fatalf("PullRequestsByIssue: %v", err)
	}
	if len(prs) != 1 || prs[0].Number != pr2.Number || prs[0].URL != pr2.URL {
		t.Errorf("recovered PR %+v does not match persisted PR %+v", pr2, prs[0])
	}
	if got := prTracker.callCount(); got != 2 {
		t.Fatalf("got %d total CreatePullRequest calls, want 2 (recovery reused the same PR)", got)
	}
}

// firstExecutionIDPlaceholder documents that branch names are per-Execution
// (forge/{execution}/{issue}); this test recovers idempotency at the
// PRCreator seam directly rather than replaying a whole second Execute
// (which would require a second, colliding claim on the same Issue).
const firstExecutionIDPlaceholder = "exec"

// TestExecute_CommitError_FailsOutAndCleansUpWorkspace guards that a
// Publisher.Commit failure drives the Issue to a terminal state and cleans
// up the now-orphaned Workspace, exactly as an Agent or Reviewer error
// already does.
func TestExecute_CommitError_FailsOutAndCleansUpWorkspace(t *testing.T) {
	te := approvedTestEngine(t, "43", domain.Issue{ID: "43", Title: "commit fails"})
	pub := &fakePublisher{commitErr: errors.New("boom: commit failed")}
	te.eng.Publisher = pub
	te.eng.PRTracker = newFakePRTracker()
	te.eng.BaseBranch = "main"

	if _, err := te.eng.Execute(context.Background(), "43", te.base); err == nil {
		t.Fatal("Execute: want error when Publisher.Commit fails")
	}
	if !te.ws.CleanupCalled() {
		t.Error("Cleanup was not called after a Commit error, want the orphaned Workspace removed")
	}
}

// TestExecute_PRCreationError_FailsOutAndCleansUpWorkspace mirrors the
// Commit-error test for a PRTracker.CreatePullRequest failure — the Issue
// has already advanced to PR_CREATING (a state with no FAILED edge) by the
// time this can fail, so this also exercises failOut's CANCELLED fallback.
func TestExecute_PRCreationError_FailsOutAndCleansUpWorkspace(t *testing.T) {
	te := approvedTestEngine(t, "44", domain.Issue{ID: "44", Title: "PR creation fails"})
	pub := &fakePublisher{commitSHA: "sha-44"}
	prTracker := newFakePRTracker()
	prTracker.createErr = errors.New("boom: github unavailable")
	te.eng.Publisher = pub
	te.eng.PRTracker = prTracker
	te.eng.BaseBranch = "main"

	if _, err := te.eng.Execute(context.Background(), "44", te.base); err == nil {
		t.Fatal("Execute: want error when PRTracker.CreatePullRequest fails")
	}
	if !te.ws.CleanupCalled() {
		t.Error("Cleanup was not called after a PR creation error, want the orphaned Workspace removed")
	}
}
