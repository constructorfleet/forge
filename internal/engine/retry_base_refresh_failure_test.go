package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
)

// nonRebasingWorkspaces wraps another WorkspaceCreator but deliberately does
// not implement WorkspaceRebaser, standing in for a backend that lacks
// Rebase support (the ":171" path in refreshRetryBase).
type nonRebasingWorkspaces struct {
	engine.WorkspaceCreator
}

var _ engine.WorkspaceCreator = nonRebasingWorkspaces{}

// failingRebaser reports a non-conflict error from Rebase, standing in for
// a Workspace backend whose rebase itself fails (":177" in
// refreshRetryBase), as opposed to succeeding with reported conflicts.
type failingRebaser struct {
	engine.WorkspaceCreator
	err error
}

func (f failingRebaser) Rebase(ctx context.Context, executionID, issueID, newBase string) ([]string, error) {
	return nil, f.err
}

var _ engine.WorkspaceRebaser = failingRebaser{}

// lastBaseRefreshFailedEvent returns the most recent worker.base_refresh_failed
// event recorded for issueID, and whether one was found.
func lastBaseRefreshFailedEvent(t *testing.T, ctx context.Context, te testEngine, executionID, issueID string) (string, bool) {
	t.Helper()
	events, err := te.store.EventsByIssue(ctx, executionID, issueID)
	if err != nil {
		t.Fatalf("EventsByIssue: %v", err)
	}
	var last string
	var found bool
	for _, e := range events {
		if e.Type == "worker.base_refresh_failed" {
			last = e.Data
			found = true
		}
	}
	return last, found
}

// TestRetryIssue_ResolveTargetTipFailureRecordsEvent confirms a
// TargetTip.CurrentTip failure -- previously a bare error with no trace in
// the store -- appends a worker.base_refresh_failed event naming the
// reason, so an observer reading only `events` can tell a refused retry
// from one that never ran.
func TestRetryIssue_ResolveTargetTipFailureRecordsEvent(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"80": {ID: "80", Title: "Target tip resolve failure"},
	})
	te.fake.ProgramResult("80", agent.AgentResult{Status: agent.StatusFailed, Summary: "boom"})

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "80", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateFailed {
		t.Fatalf("initial state = %s, want FAILED", result.Issue.State)
	}

	wantErr := errors.New("target tip unreachable")
	te.eng.TargetTip = engine.TargetTipResolverFunc(func(context.Context) (string, error) {
		return "", wantErr
	})

	_, err = te.eng.RetryIssue(ctx, result.ExecutionID, "80")
	if err == nil {
		t.Fatal("RetryIssue err = nil, want a resolve-tip failure")
	}

	data, found := lastBaseRefreshFailedEvent(t, ctx, te, result.ExecutionID, "80")
	if !found {
		t.Fatal("no worker.base_refresh_failed event recorded for a CurrentTip failure")
	}
	if !strings.Contains(data, wantErr.Error()) {
		t.Fatalf("worker.base_refresh_failed data = %s, want it to mention %q", data, wantErr.Error())
	}
}

// TestRetryIssue_AncestryCheckErrorRecordsEvent confirms an AncestorChecker
// error -- as opposed to a definite "not an ancestor" verdict -- also
// appends a worker.base_refresh_failed event, distinguishing an
// infrastructure fault from the deliberate refusal covered by
// TestRetryIssue_RefusesBaseRefreshThatWouldDropMergedDependency.
func TestRetryIssue_AncestryCheckErrorRecordsEvent(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"81": {ID: "81", Title: "Ancestry check error"},
	})
	te.fake.ProgramResult("81", agent.AgentResult{Status: agent.StatusFailed, Summary: "boom"})

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "81", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	newTip := advanceTarget(t, te.eng.RepoRoot, "advance for ancestry error")
	te.eng.TargetTip = engine.TargetTipResolverFunc(func(context.Context) (string, error) {
		return newTip, nil
	})
	wantErr := errors.New("git merge-base exploded")
	te.eng.Ancestry = fakeAncestorChecker{err: wantErr}

	_, err = te.eng.RetryIssue(ctx, result.ExecutionID, "81")
	if err == nil {
		t.Fatal("RetryIssue err = nil, want an ancestry-check failure")
	}

	data, found := lastBaseRefreshFailedEvent(t, ctx, te, result.ExecutionID, "81")
	if !found {
		t.Fatal("no worker.base_refresh_failed event recorded for an IsAncestor error")
	}
	if !strings.Contains(data, wantErr.Error()) {
		t.Fatalf("worker.base_refresh_failed data = %s, want it to mention %q", data, wantErr.Error())
	}
}

// TestRetryIssue_AncestryRefusalRecordsDistinctEvent confirms the ancestry
// refusal -- an expected outcome, not a fault -- records its own event type
// (worker.base_refresh_refused) rather than the fault event
// worker.base_refresh_failed used by the other paths.
func TestRetryIssue_AncestryRefusalRecordsDistinctEvent(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"82": {ID: "82", Title: "Ancestry refusal event"},
	})
	te.fake.ProgramResult("82", agent.AgentResult{Status: agent.StatusFailed, Summary: "boom"})

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "82", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	newTip := advanceTarget(t, te.eng.RepoRoot, "diverged advance for event check")
	te.eng.TargetTip = engine.TargetTipResolverFunc(func(context.Context) (string, error) {
		return newTip, nil
	})
	te.eng.Ancestry = fakeAncestorChecker{ok: false}

	if _, err := te.eng.RetryIssue(ctx, result.ExecutionID, "82"); err == nil {
		t.Fatal("RetryIssue err = nil, want a refusal error")
	}

	events, err := te.store.EventsByIssue(ctx, result.ExecutionID, "82")
	if err != nil {
		t.Fatalf("EventsByIssue: %v", err)
	}
	var sawRefusal, sawFailed bool
	for _, e := range events {
		switch e.Type {
		case "worker.base_refresh_refused":
			sawRefusal = true
			if !strings.Contains(e.Data, newTip) {
				t.Errorf("worker.base_refresh_refused data = %s, want it to mention new base %s", e.Data, newTip)
			}
		case "worker.base_refresh_failed":
			sawFailed = true
		}
	}
	if !sawRefusal {
		t.Fatalf("no worker.base_refresh_refused event recorded for the ancestry refusal; events=%+v", events)
	}
	if sawFailed {
		t.Fatalf("ancestry refusal recorded worker.base_refresh_failed, want only worker.base_refresh_refused; events=%+v", events)
	}
}

// TestRetryIssue_RebaseUnsupportedRecordsEvent confirms a Workspace backend
// that does not implement WorkspaceRebaser records the failure instead of
// returning a bare error.
func TestRetryIssue_RebaseUnsupportedRecordsEvent(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"83": {ID: "83", Title: "Rebase unsupported"},
	})
	te.fake.ProgramResult("83", agent.AgentResult{Status: agent.StatusFailed, Summary: "boom"})

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "83", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	newTip := advanceTarget(t, te.eng.RepoRoot, "advance for rebase-unsupported")
	te.eng.TargetTip = engine.TargetTipResolverFunc(func(context.Context) (string, error) {
		return newTip, nil
	})
	te.eng.Ancestry = fakeAncestorChecker{ok: true}
	te.eng.Workspaces = nonRebasingWorkspaces{WorkspaceCreator: te.ws}

	_, err = te.eng.RetryIssue(ctx, result.ExecutionID, "83")
	if err == nil {
		t.Fatal("RetryIssue err = nil, want a rebase-unsupported failure")
	}

	data, found := lastBaseRefreshFailedEvent(t, ctx, te, result.ExecutionID, "83")
	if !found {
		t.Fatal("no worker.base_refresh_failed event recorded when Workspaces does not support Rebase")
	}
	if !strings.Contains(data, "rebase") {
		t.Fatalf("worker.base_refresh_failed data = %s, want it to mention rebase support", data)
	}
}

// TestRetryIssue_RebaseErrorRecordsEvent confirms a Rebase call that fails
// outright (as opposed to succeeding with reported conflicts) also records
// the failure.
func TestRetryIssue_RebaseErrorRecordsEvent(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"84": {ID: "84", Title: "Rebase error"},
	})
	te.fake.ProgramResult("84", agent.AgentResult{Status: agent.StatusFailed, Summary: "boom"})

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "84", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	newTip := advanceTarget(t, te.eng.RepoRoot, "advance for rebase error")
	te.eng.TargetTip = engine.TargetTipResolverFunc(func(context.Context) (string, error) {
		return newTip, nil
	})
	te.eng.Ancestry = fakeAncestorChecker{ok: true}
	wantErr := errors.New("rebase backend unavailable")
	te.eng.Workspaces = failingRebaser{WorkspaceCreator: te.ws, err: wantErr}

	_, err = te.eng.RetryIssue(ctx, result.ExecutionID, "84")
	if err == nil {
		t.Fatal("RetryIssue err = nil, want a rebase failure")
	}

	data, found := lastBaseRefreshFailedEvent(t, ctx, te, result.ExecutionID, "84")
	if !found {
		t.Fatal("no worker.base_refresh_failed event recorded for a Rebase error")
	}
	if !strings.Contains(data, wantErr.Error()) {
		t.Fatalf("worker.base_refresh_failed data = %s, want it to mention %q", data, wantErr.Error())
	}
}
