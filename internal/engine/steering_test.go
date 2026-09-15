package engine_test

import (
	"context"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/steering"
	"github.com/Teagan42/forge/internal/storage"
)

// enqueueOnFirstCallAgent wraps an agent.Agent and enqueues one steering
// Message on queue during its first Execute call only, before delegating to
// inner — a stand-in for a steering message arriving from another goroutine
// while that first step's Agent invocation is still in flight.
type enqueueOnFirstCallAgent struct {
	inner agent.Agent
	queue *steering.Queue
	msg   steering.Message
	calls int
}

func (a *enqueueOnFirstCallAgent) Execute(ctx context.Context, req agent.AgentRequest) (agent.AgentResult, error) {
	a.calls++
	if a.calls == 1 {
		a.queue.Enqueue(a.msg)
	}
	return a.inner.Execute(ctx, req)
}

var _ agent.Agent = (*enqueueOnFirstCallAgent)(nil)

// TestRunRepairLoop_DrainsQueueOnlyBetweenSteps proves TKT-003's acceptance
// criteria: a steering Message enqueued while a step's Agent invocation is
// in progress is absent from that same step's Feedback, and appears only in
// the next step's Feedback, once that step begins — not mid-step.
func TestRunRepairLoop_DrainsQueueOnlyBetweenSteps(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"50": {ID: "50"},
	})
	te.fake.ProgramResult("50", agent.AgentResult{Status: agent.StatusImplemented})
	te.eng.Config.Quality.Gates = []config.QualityGate{{Name: "test", Command: "make test"}}
	te.eng.Config.Retry = domain.RetryLimits{Gate: 1, Review: 1, CI: 1}
	runner := &flakyRunner{failUntil: 1}
	te.gates.Set(runner)

	queue := steering.NewQueue()
	te.eng.Steering = queue
	wrapped := &enqueueOnFirstCallAgent{
		inner: te.eng.Agent,
		queue: queue,
		msg:   steering.Message{Text: "steer during step 1"},
	}
	te.eng.Agent = wrapped

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "50", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateCommitting {
		t.Fatalf("final state = %s, want COMMITTING", result.Issue.State)
	}

	invocations := te.fake.Invocations()
	if len(invocations) != 2 {
		t.Fatalf("got %d agent invocations, want 2 (initial + 1 repair)", len(invocations))
	}

	for _, fb := range invocations[0].Feedback {
		if fb.Source == agent.FeedbackSourceSteering {
			t.Fatalf("first invocation Feedback = %+v, want no STEERING entry (message enqueued mid-step)", invocations[0].Feedback)
		}
	}

	var steeringFeedback []agent.Feedback
	for _, fb := range invocations[1].Feedback {
		if fb.Source == agent.FeedbackSourceSteering {
			steeringFeedback = append(steeringFeedback, fb)
		}
	}
	if len(steeringFeedback) != 1 {
		t.Fatalf("repair invocation Feedback = %+v, want exactly 1 STEERING entry", invocations[1].Feedback)
	}
	if steeringFeedback[0].Message != "steer during step 1" {
		t.Errorf("STEERING Feedback.Message = %q, want %q", steeringFeedback[0].Message, "steer during step 1")
	}

	if drained := queue.DrainAll(); drained != "" {
		t.Errorf("queue.DrainAll() after loop = %q, want empty (already drained at the step boundary)", drained)
	}
}

// TestRunRepairLoop_EmptyQueueLeavesFeedbackUnchanged proves TKT-003's third
// acceptance criterion: an Engine with a Steering Queue configured but empty
// at a step boundary behaves exactly like one with no Queue at all.
func TestRunRepairLoop_EmptyQueueLeavesFeedbackUnchanged(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"51": {ID: "51"},
	})
	te.fake.ProgramResult("51", agent.AgentResult{Status: agent.StatusImplemented})
	te.eng.Config.Quality.Gates = []config.QualityGate{{Name: "test", Command: "make test"}}
	te.eng.Config.Retry = domain.RetryLimits{Gate: 1, Review: 1, CI: 1}
	runner := &flakyRunner{failUntil: 1}
	te.gates.Set(runner)
	te.eng.Steering = steering.NewQueue()

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "51", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateCommitting {
		t.Fatalf("final state = %s, want COMMITTING", result.Issue.State)
	}

	invocations := te.fake.Invocations()
	if len(invocations) != 2 {
		t.Fatalf("got %d agent invocations, want 2 (initial + 1 repair)", len(invocations))
	}
	if len(invocations[1].Feedback) != 1 || invocations[1].Feedback[0].Source != agent.FeedbackSourceGate {
		t.Fatalf("repair invocation Feedback = %+v, want exactly 1 GATE entry, no STEERING", invocations[1].Feedback)
	}
}

// TestRunRepairLoop_PersistsSteeringMessageToTranscript proves TKT-007's
// acceptance criteria: a Message drained from the Queue and injected into
// the next step's Feedback is also written to the transcript store, tagged
// with a Type distinguishing it from agent-generated transcript entries.
func TestRunRepairLoop_PersistsSteeringMessageToTranscript(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"52": {ID: "52"},
	})
	te.fake.ProgramResult("52", agent.AgentResult{Status: agent.StatusImplemented})
	te.eng.Config.Quality.Gates = []config.QualityGate{{Name: "test", Command: "make test"}}
	te.eng.Config.Retry = domain.RetryLimits{Gate: 1, Review: 1, CI: 1}
	runner := &flakyRunner{failUntil: 1}
	te.gates.Set(runner)

	queue := steering.NewQueue()
	te.eng.Steering = queue
	wrapped := &enqueueOnFirstCallAgent{
		inner: te.eng.Agent,
		queue: queue,
		msg:   steering.Message{Text: "steer during step 1"},
	}
	te.eng.Agent = wrapped

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "52", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateCommitting {
		t.Fatalf("final state = %s, want COMMITTING", result.Issue.State)
	}

	events, err := te.store.TranscriptEventsByIssue(ctx, result.ExecutionID, "52")
	if err != nil {
		t.Fatalf("TranscriptEventsByIssue: %v", err)
	}
	var steeringEvents []storage.TranscriptEvent
	for _, event := range events {
		if event.Type == "STEERING" {
			steeringEvents = append(steeringEvents, event)
		}
	}
	if len(steeringEvents) != 1 {
		t.Fatalf("got %d STEERING transcript events, want 1 (from %+v)", len(steeringEvents), events)
	}
	if steeringEvents[0].Text != "steer during step 1" {
		t.Errorf("STEERING transcript event Text = %q, want %q", steeringEvents[0].Text, "steer during step 1")
	}
}
