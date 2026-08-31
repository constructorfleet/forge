package engine_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/tracker"
)

// fakeFollowUpTracker is a minimal engine.FollowUpTracker double: it never
// hits a real tracker, and records every CreateIssue/AddLabel call so tests
// can assert automatic self reporting (issue 141) files the right Issues.
type fakeFollowUpTracker struct {
	mu        sync.Mutex
	created   []tracker.IssueRequest
	labels    map[string][]string
	nextID    int
	createErr error
}

func newFakeFollowUpTracker() *fakeFollowUpTracker {
	return &fakeFollowUpTracker{labels: map[string][]string{}}
}

func (f *fakeFollowUpTracker) CreateIssue(_ context.Context, req tracker.IssueRequest) (tracker.CreatedIssue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return tracker.CreatedIssue{}, f.createErr
	}
	f.created = append(f.created, req)
	f.nextID++
	id := "followup-" + string(rune('0'+f.nextID))
	return tracker.CreatedIssue{ID: id, URL: "https://example.invalid/issues/" + id}, nil
}

func (f *fakeFollowUpTracker) AddLabel(_ context.Context, id string, label string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.labels[id] = append(f.labels[id], label)
	return nil
}

func (f *fakeFollowUpTracker) Created() []tracker.IssueRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]tracker.IssueRequest(nil), f.created...)
}

func (f *fakeFollowUpTracker) Labels(id string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.labels[id]...)
}

var _ engine.FollowUpTracker = (*fakeFollowUpTracker)(nil)

func TestExecute_FollowUpsAreFiledAsNewIssuesAndLabeled(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"42": {ID: "42"},
	})
	fu := newFakeFollowUpTracker()
	te.eng.FollowUpTracker = fu
	te.fake.ProgramResult("42", agent.AgentResult{
		Status:  agent.StatusImplemented,
		Summary: "did the thing",
		FollowUps: []agent.FollowUpReport{
			{Title: "Flaky test noticed", Body: "TestFoo occasionally times out."},
		},
	})

	ctx := context.Background()
	if _, err := te.eng.Execute(ctx, "42", te.base); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	created := fu.Created()
	if len(created) != 1 {
		t.Fatalf("created = %+v, want 1 follow-up issue", created)
	}
	if created[0].Title != "Flaky test noticed" {
		t.Errorf("created[0].Title = %q, want %q", created[0].Title, "Flaky test noticed")
	}
	if created[0].Body == "" {
		t.Errorf("created[0].Body is empty, want the follow-up body")
	}

	labels := fu.Labels("followup-1")
	if len(labels) != 1 || labels[0] != te.eng.Config.FollowUp.Label {
		t.Errorf("Labels(followup-1) = %v, want [%s]", labels, te.eng.Config.FollowUp.Label)
	}
}

func TestExecute_NoFollowUpsFilesNoIssues(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"42": {ID: "42"},
	})
	fu := newFakeFollowUpTracker()
	te.eng.FollowUpTracker = fu
	te.fake.ProgramResult("42", agent.AgentResult{Status: agent.StatusImplemented, Summary: "did the thing"})

	ctx := context.Background()
	if _, err := te.eng.Execute(ctx, "42", te.base); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if created := fu.Created(); len(created) != 0 {
		t.Errorf("created = %+v, want none", created)
	}
}

func TestExecute_FollowUpTrackerFailureDoesNotFailTheIssue(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"42": {ID: "42"},
	})
	fu := newFakeFollowUpTracker()
	fu.createErr = errors.New("tracker unavailable")
	te.eng.FollowUpTracker = fu
	te.fake.ProgramResult("42", agent.AgentResult{
		Status:  agent.StatusImplemented,
		Summary: "did the thing",
		FollowUps: []agent.FollowUpReport{
			{Title: "Flaky test noticed", Body: "TestFoo occasionally times out."},
		},
	})

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "42", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateCommitting {
		t.Fatalf("final state = %s, want COMMITTING despite the follow-up tracker failure", result.Issue.State)
	}

	events, err := te.store.EventsByExecution(ctx, result.ExecutionID)
	if err != nil {
		t.Fatalf("EventsByExecution: %v", err)
	}
	found := false
	for _, ev := range events {
		if ev.Type == "followup.failed" {
			found = true
		}
	}
	if !found {
		t.Errorf("events = %+v, want a followup.failed event", events)
	}
}

func TestExecute_NilFollowUpTrackerIsANoOp(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"42": {ID: "42"},
	})
	// te.eng.FollowUpTracker left nil deliberately.
	te.fake.ProgramResult("42", agent.AgentResult{
		Status:  agent.StatusImplemented,
		Summary: "did the thing",
		FollowUps: []agent.FollowUpReport{
			{Title: "Flaky test noticed", Body: "TestFoo occasionally times out."},
		},
	})

	ctx := context.Background()
	if _, err := te.eng.Execute(ctx, "42", te.base); err != nil {
		t.Fatalf("Execute: %v", err)
	}
}
