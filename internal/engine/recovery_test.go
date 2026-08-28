package engine_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
)

type fakeCIWaiter struct {
	calls []struct {
		executionID string
		issueID     string
	}
	state domain.IssueState
	err   error
	store storage.Store
}

func (f *fakeCIWaiter) Wait(ctx context.Context, executionID, issueID string) (domain.IssueState, error) {
	f.calls = append(f.calls, struct {
		executionID string
		issueID     string
	}{executionID: executionID, issueID: issueID})
	if f.err == nil && f.store != nil && f.state != "" && f.state != domain.StateCIPending {
		if _, err := f.store.TransitionIssue(ctx, executionID, issueID, f.state); err != nil {
			return "", err
		}
	}
	return f.state, f.err
}

func seedRecoveryExecution(t *testing.T, te testEngine, issue domain.Issue, state domain.IssueState, ownerPID int) (string, domain.Workspace) {
	t.Helper()
	ctx := context.Background()
	executionID := "exec-recovery-" + issue.ID
	exec := domain.Execution{
		ID:           executionID,
		BaseRevision: te.base,
		StartedAt:    time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC),
	}
	if err := te.store.CreateExecution(ctx, exec); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	issue.ExecutionID = executionID
	issue.State = state
	issue.Scope = domain.ScopeManaged
	issue.RetryBudget = domain.NewRetryBudget(domain.RetryLimits{Gate: 3, Review: 3, CI: 3})
	if err := te.store.CreateIssue(ctx, issue); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := te.store.AppendEvent(ctx, storage.Event{
		ExecutionID: executionID,
		IssueID:     issue.ID,
		Type:        "worker.base_captured",
		Data:        `{"base":"` + te.base + `"}`,
		OccurredAt:  time.Date(2026, 8, 28, 12, 1, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("AppendEvent(worker.base_captured): %v", err)
	}
	if err := te.store.ClaimIssue(ctx, executionID, issue.ID, "worker-"+issue.ID); err != nil {
		t.Fatalf("ClaimIssue: %v", err)
	}
	if err := te.store.UpdateWorkerOwner(ctx, executionID, issue.ID, ownerPID); err != nil {
		t.Fatalf("UpdateWorkerOwner: %v", err)
	}

	ws, err := te.ws.mgr.Create(ctx, executionID, issue.ID, te.base)
	if err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	if err := te.store.RecordWorkspace(ctx, executionID, ws); err != nil {
		t.Fatalf("RecordWorkspace: %v", err)
	}
	if err := te.store.AppendEvent(ctx, storage.Event{
		ExecutionID: executionID,
		IssueID:     issue.ID,
		Type:        "workspace.created",
		Data:        `{"path":"` + ws.Path + `","branch":"` + ws.Branch + `"}`,
		OccurredAt:  time.Date(2026, 8, 28, 12, 2, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("AppendEvent(workspace.created): %v", err)
	}
	return executionID, ws
}

func TestResumeExecution_ImplementationCrash_ReusesWorkspaceAndCompletes(t *testing.T) {
	te := approvedTestEngine(t, "51", domain.Issue{ID: "51", Title: "Recover implementation"})
	pub := &fakePublisher{commitSHA: "sha-51"}
	prTracker := newFakePRTracker()
	ciWaiter := &fakeCIWaiter{state: domain.StateDone, store: te.store}
	te.eng.Publisher = pub
	te.eng.PRTracker = prTracker
	te.eng.BaseBranch = "main"
	te.eng.CIWaiter = ciWaiter
	te.fake = agent.NewFakeAgent()
	te.fake.ProgramResult("51", agent.AgentResult{Status: agent.StatusImplemented, Summary: "continued"})
	te.eng.Agent = te.fake

	executionID, ws := seedRecoveryExecution(t, te, domain.Issue{ID: "51", Title: "Recover implementation"}, domain.StateImplementing, 999999)

	result, err := te.eng.ResumeExecution(context.Background(), executionID)
	if err != nil {
		t.Fatalf("ResumeExecution: %v", err)
	}
	if len(result.Issues) != 1 {
		t.Fatalf("got %d issues, want 1", len(result.Issues))
	}
	if result.Issues[0].State != domain.StateDone {
		t.Fatalf("final state = %s, want DONE", result.Issues[0].State)
	}

	invocations := te.fake.Invocations()
	if len(invocations) != 1 {
		t.Fatalf("got %d agent invocations, want 1", len(invocations))
	}
	if invocations[0].WorkspacePath != ws.Path {
		t.Fatalf("WorkspacePath = %s, want %s", invocations[0].WorkspacePath, ws.Path)
	}

	issue, err := te.store.GetIssue(context.Background(), executionID, "51")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.State != domain.StateDone {
		t.Fatalf("persisted state = %s, want DONE", issue.State)
	}
	if len(ciWaiter.calls) != 1 {
		t.Fatalf("CI waiter calls = %d, want 1", len(ciWaiter.calls))
	}
}

func TestResumeExecution_CIPendingCrash_ResumesMonitoringWithoutReinvokingAgent(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{"52": {ID: "52"}})
	ciWaiter := &fakeCIWaiter{state: domain.StateDone, store: te.store}
	te.eng.CIWaiter = ciWaiter

	executionID := "exec-recovery-52"
	ctx := context.Background()
	if err := te.store.CreateExecution(ctx, domain.Execution{
		ID:           executionID,
		BaseRevision: te.base,
		StartedAt:    time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	if err := te.store.CreateIssue(ctx, domain.Issue{
		ID:          "52",
		ExecutionID: executionID,
		Title:       "Resume CI",
		State:       domain.StateCIPending,
		Scope:       domain.ScopeManaged,
		RetryBudget: domain.NewRetryBudget(domain.RetryLimits{Gate: 3, Review: 3, CI: 3}),
	}); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := te.store.RecordPullRequest(ctx, storage.PullRequest{
		ExecutionID: executionID,
		IssueID:     "52",
		Number:      52,
		URL:         "https://example.invalid/pr/52",
		CommitSHA:   "sha-52",
		CreatedAt:   time.Date(2026, 8, 28, 12, 1, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("RecordPullRequest: %v", err)
	}

	result, err := te.eng.ResumeExecution(ctx, executionID)
	if err != nil {
		t.Fatalf("ResumeExecution: %v", err)
	}
	if len(result.Issues) != 1 || result.Issues[0].State != domain.StateDone {
		t.Fatalf("issues = %+v, want one DONE issue", result.Issues)
	}
	if got := len(te.fake.Invocations()); got != 0 {
		t.Fatalf("agent invocations = %d, want 0", got)
	}
	if len(ciWaiter.calls) != 1 {
		t.Fatalf("CI waiter calls = %d, want 1", len(ciWaiter.calls))
	}
}

func TestHandleNeedsInfo_ReleasesWorkerClaim(t *testing.T) {
	eng, store, _, fake, base := newNeedsInfoTestEngine(t, map[string]domain.Issue{
		"53": {ID: "53"},
	})
	fake.ProgramResult("53", agent.AgentResult{
		Status:    agent.StatusNeedsInfo,
		NeedsInfo: &agent.NeedsInfoDetail{Question: "which flag?"},
	})

	result, err := eng.Execute(context.Background(), "53", base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateNeedsInfo {
		t.Fatalf("state = %s, want NEEDS_INFO", result.Issue.State)
	}
	if _, err := store.WorkerClaim(context.Background(), result.ExecutionID, "53"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("WorkerClaim after NEEDS_INFO = %v, want ErrNotFound", err)
	}
	if err := store.ClaimIssue(context.Background(), result.ExecutionID, "53", "worker-53-resumed"); err != nil {
		t.Fatalf("reclaim after NEEDS_INFO: %v", err)
	}
}

func TestResumeExecution_LiveForeignOwnerDoesNotReleaseClaim(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{"53b": {ID: "53b"}})
	te.eng.OwnerPID = func() int { return 111 }
	te.eng.ProcessRunning = func(pid int) (bool, error) {
		return pid == 222, nil
	}

	executionID, _ := seedRecoveryExecution(t, te, domain.Issue{ID: "53b", Title: "Resume owned issue"}, domain.StateImplementing, 222)
	_, err := te.eng.ResumeExecution(context.Background(), executionID)
	if err == nil {
		t.Fatal("ResumeExecution should reject a live foreign owner")
	}

	claim, err := te.store.WorkerClaim(context.Background(), executionID, "53b")
	if err != nil {
		t.Fatalf("WorkerClaim after rejected resume: %v", err)
	}
	if claim.OwnerPID != 222 {
		t.Fatalf("OwnerPID after rejected resume = %d, want 222", claim.OwnerPID)
	}
}

func TestResumeExecution_PRCreatingRecovery_ReusesExistingPRWithoutDuplicateRecord(t *testing.T) {
	te := approvedTestEngine(t, "54", domain.Issue{ID: "54", Title: "Resume PR"})
	pub := &fakePublisher{commitSHA: "sha-54"}
	prTracker := newFakePRTracker()
	te.eng.Publisher = pub
	te.eng.PRTracker = prTracker
	te.eng.BaseBranch = "main"

	executionID, ws := seedRecoveryExecution(t, te, domain.Issue{ID: "54", Title: "Resume PR"}, domain.StatePRCreating, 999999)
	existing, err := prTracker.CreatePullRequest(context.Background(), tracker.PullRequestRequest{
		Base:  "main",
		Head:  ws.Branch,
		Title: "Resume PR",
		Body:  "body",
	})
	if err != nil {
		t.Fatalf("seed CreatePullRequest: %v", err)
	}
	if err := te.store.RecordPullRequest(context.Background(), storage.PullRequest{
		ExecutionID: executionID,
		IssueID:     "54",
		Number:      existing.Number,
		URL:         existing.URL,
		CommitSHA:   "sha-54",
		CreatedAt:   time.Date(2026, 8, 28, 12, 3, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("seed RecordPullRequest: %v", err)
	}

	result, err := te.eng.ResumeExecution(context.Background(), executionID)
	if err != nil {
		t.Fatalf("ResumeExecution: %v", err)
	}
	if len(result.Issues) != 1 || result.Issues[0].State != domain.StateCIPending {
		t.Fatalf("issues = %+v, want one CI_PENDING issue", result.Issues)
	}
	prs, err := te.store.PullRequestsByIssue(context.Background(), executionID, "54")
	if err != nil {
		t.Fatalf("PullRequestsByIssue: %v", err)
	}
	if len(prs) != 1 {
		t.Fatalf("got %d PR rows, want 1 after recovery", len(prs))
	}
}
