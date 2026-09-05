package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/storage"
)

// TestRetryIssue_ApplyTransitionEffectsFailureIsDeferredNotFailed covers
// issue 555: a status-reflection failure after RetryIssue's claim commits
// must not report a bare error. The claim already left the Issue READY with
// a reset budget and no Worker claim, which the scheduler picks up as a
// normal retry, so RetryIssue must wrap the failure in
// RetryStartDeferredError instead of reporting a plain failed retry.
func TestRetryIssue_ApplyTransitionEffectsFailureIsDeferredNotFailed(t *testing.T) {
	cfg := statusReflectionConfig()
	eng, trk, fake, base := newStatusReflectionTestEngine(t, cfg, map[string]domain.Issue{
		"90": {ID: "90", Title: "Deferred on tracker failure"},
	})
	fake.ProgramResult("90", agent.AgentResult{Status: agent.StatusFailed, Summary: "boom"})

	ctx := context.Background()
	result, err := eng.Execute(ctx, "90", base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateFailed {
		t.Fatalf("initial state = %s, want FAILED", result.Issue.State)
	}
	if labels := trk.Labels("90"); len(labels) != 1 || labels[0] != cfg.StatusReflection.FailedLabel {
		t.Fatalf("labels before retry = %v, want [%s]", labels, cfg.StatusReflection.FailedLabel)
	}

	wantErr := errors.New("tracker unreachable")
	trk.removeLabelErr = wantErr

	_, err = eng.RetryIssue(ctx, result.ExecutionID, "90")
	if err == nil {
		t.Fatal("RetryIssue err = nil, want a deferred-start error")
	}
	var deferred *engine.RetryStartDeferredError
	if !errors.As(err, &deferred) {
		t.Fatalf("RetryIssue error = %v, want it to wrap RetryStartDeferredError", err)
	}
	if !strings.Contains(deferred.Err.Error(), wantErr.Error()) {
		t.Fatalf("deferred.Err = %v, want it to mention %q", deferred.Err, wantErr.Error())
	}

	issue, err := eng.Store.GetIssue(ctx, result.ExecutionID, "90")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.State != domain.StateReady {
		t.Fatalf("persisted state after deferred start = %s, want READY", issue.State)
	}
}

// failingAppendEventStore wraps a storage.Store and fails only the named
// Event type, standing in for an infrastructure fault (e.g. the database
// going away) hitting Engine.appendEvent specifically, without disturbing
// every other Store call RetryIssue makes along the way.
type failingAppendEventStore struct {
	storage.Store
	failType string
	err      error
}

func (f *failingAppendEventStore) AppendEvent(ctx context.Context, ev storage.Event) error {
	if ev.Type == f.failType {
		return f.err
	}
	return f.Store.AppendEvent(ctx, ev)
}

// TestRetryIssue_RetryRequestedEventFailureIsDeferredNotFailed covers the
// same issue 555 gap for the appendEvent("issue.retry_requested") step: a
// Store fault recording that informational Event must not read as a failed
// retry either, since the claim it followed already committed.
func TestRetryIssue_RetryRequestedEventFailureIsDeferredNotFailed(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"91": {ID: "91", Title: "Deferred on event failure"},
	})
	te.fake.ProgramResult("91", agent.AgentResult{Status: agent.StatusFailed, Summary: "boom"})

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "91", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateFailed {
		t.Fatalf("initial state = %s, want FAILED", result.Issue.State)
	}

	wantErr := errors.New("database unavailable")
	te.eng.Store = &failingAppendEventStore{Store: te.store, failType: "issue.retry_requested", err: wantErr}

	_, err = te.eng.RetryIssue(ctx, result.ExecutionID, "91")
	if err == nil {
		t.Fatal("RetryIssue err = nil, want a deferred-start error")
	}
	var deferred *engine.RetryStartDeferredError
	if !errors.As(err, &deferred) {
		t.Fatalf("RetryIssue error = %v, want it to wrap RetryStartDeferredError", err)
	}
	if !strings.Contains(deferred.Err.Error(), wantErr.Error()) {
		t.Fatalf("deferred.Err = %v, want it to mention %q", deferred.Err, wantErr.Error())
	}

	issue, err := te.store.GetIssue(ctx, result.ExecutionID, "91")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.State != domain.StateReady {
		t.Fatalf("persisted state after deferred start = %s, want READY", issue.State)
	}
}

// failingRecoveryWorkspaces wraps another engine.WorkspaceCreator but fails
// both Validate and Cleanup, standing in for a Workspace backend that has
// gone unreachable by the time resumeIssue re-enters it during a retry.
type failingRecoveryWorkspaces struct {
	engine.WorkspaceCreator
	err error
}

func (f failingRecoveryWorkspaces) Validate(context.Context, string, string) (domain.Workspace, error) {
	return domain.Workspace{}, f.err
}

func (f failingRecoveryWorkspaces) Cleanup(context.Context, string, string) error {
	return f.err
}

// TestRetryIssue_ResumeIssueFailureLeavesIssueStuckMidResume covers the
// resumeIssue leg of issue 555: resumeIssue's re-entry to a READY Issue
// claims it and transitions it to CLAIMED and then PREPARING before it can
// fail further in trying to recover the Workspace. That failure must not
// read as a harmless deferred start, since the Issue is no longer in the
// READY/unclaimed shape the scheduler treats as ready to run — it needs an
// explicit `forge resume` instead.
func TestRetryIssue_ResumeIssueFailureLeavesIssueStuckMidResume(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"92": {ID: "92", Title: "Stuck on resume failure"},
	})
	te.fake.ProgramResult("92", agent.AgentResult{Status: agent.StatusFailed, Summary: "boom"})

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "92", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateFailed {
		t.Fatalf("initial state = %s, want FAILED", result.Issue.State)
	}

	wantErr := errors.New("workspace unreachable")
	te.eng.Workspaces = failingRecoveryWorkspaces{WorkspaceCreator: te.ws, err: wantErr}

	_, err = te.eng.RetryIssue(ctx, result.ExecutionID, "92")
	if err == nil {
		t.Fatal("RetryIssue err = nil, want a stuck-mid-resume error")
	}
	var stuck *engine.RetryResumeStuckError
	if !errors.As(err, &stuck) {
		t.Fatalf("RetryIssue error = %v, want it to wrap RetryResumeStuckError", err)
	}
	if !strings.Contains(stuck.Err.Error(), wantErr.Error()) {
		t.Fatalf("stuck.Err = %v, want it to mention %q", stuck.Err, wantErr.Error())
	}
	if stuck.State == domain.StateReady {
		t.Fatalf("stuck.State = %s, want a state past READY", stuck.State)
	}

	issue, err := te.store.GetIssue(ctx, result.ExecutionID, "92")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.State != domain.StatePreparing {
		t.Fatalf("persisted state after stuck resume = %s, want PREPARING", issue.State)
	}
	if stuck.State != issue.State {
		t.Fatalf("stuck.State = %s, want it to match the persisted state %s", stuck.State, issue.State)
	}
}
