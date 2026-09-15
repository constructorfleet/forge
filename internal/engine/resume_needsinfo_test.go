package engine_test

import (
	"context"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/executeloop"
	"github.com/Teagan42/forge/internal/steering"
)

// enqueueAnswerOnFirstCallAgent wraps an agent.Agent and enqueues a
// steering.KindAnswer Message on queue during its first Execute call only,
// before delegating to inner — a stand-in for a NEEDS_INFO answer that
// arrives (via the same Enqueue/DrainAll path any steering message uses)
// while the loop is about to pause.
type enqueueAnswerOnFirstCallAgent struct {
	inner agent.Agent
	queue *steering.Queue
	msg   steering.Message
	calls int
}

func (a *enqueueAnswerOnFirstCallAgent) Execute(ctx context.Context, req agent.AgentRequest) (agent.AgentResult, error) {
	a.calls++
	if a.calls == 1 {
		a.queue.Enqueue(a.msg)
	}
	return a.inner.Execute(ctx, req)
}

var _ agent.Agent = (*enqueueAnswerOnFirstCallAgent)(nil)

// TestExecute_NeedsInfo_ResumesFromQueueAndInjectsAnswerAsContext proves
// TKT-006's headline acceptance criteria: once the loop pauses at
// NEEDS_INFO, an answer enqueued on the same Steering Queue every steering
// message uses resumes the loop in place (no separate `forge resume` call),
// draining the queue and carrying its content into the next step's
// Feedback, and the loop's status is restored to running.
func TestExecute_NeedsInfo_ResumesFromQueueAndInjectsAnswerAsContext(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"80": {ID: "80"},
	})
	te.fake.ProgramResult("80", agent.AgentResult{
		Status:    agent.StatusNeedsInfo,
		NeedsInfo: &agent.NeedsInfoDetail{Question: "which config flag?"},
	})
	te.fake.ProgramResult("80", agent.AgentResult{Status: agent.StatusImplemented})

	queue := steering.NewQueue()
	te.eng.Steering = queue
	session := executeloop.NewSession()
	te.eng.Session = session

	wrapped := &enqueueAnswerOnFirstCallAgent{
		inner: te.eng.Agent,
		queue: queue,
		msg:   steering.Message{Text: "use flag A", Kind: steering.KindAnswer},
	}
	te.eng.Agent = wrapped

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "80", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateCommitting {
		t.Fatalf("final state = %s, want COMMITTING (loop resumed and finished)", result.Issue.State)
	}

	invocations := te.fake.Invocations()
	if len(invocations) != 2 {
		t.Fatalf("got %d agent invocations, want 2 (initial NEEDS_INFO + resumed)", len(invocations))
	}

	var answerFeedback []agent.Feedback
	for _, fb := range invocations[1].Feedback {
		if fb.Source == agent.FeedbackSourceSteeringAnswer {
			answerFeedback = append(answerFeedback, fb)
		}
	}
	if len(answerFeedback) != 1 {
		t.Fatalf("resumed invocation Feedback = %+v, want exactly 1 answer entry", invocations[1].Feedback)
	}
	if answerFeedback[0].Message != "use flag A" {
		t.Errorf("answer Feedback.Message = %q, want %q", answerFeedback[0].Message, "use flag A")
	}

	if got := session.Status(); got != executeloop.StatusRunning {
		t.Errorf("Session.Status() after resume = %q, want %q", got, executeloop.StatusRunning)
	}

	if drained := queue.DrainAll(); drained != "" {
		t.Errorf("queue.DrainAll() after resume = %q, want empty (already drained)", drained)
	}
}

// TestExecute_NeedsInfo_NoSteeringQueueStaysPaused proves an Engine with no
// Steering Queue configured (nil, the zero value) behaves exactly as before
// TKT-006: NEEDS_INFO is a resting state, not an in-process wait.
func TestExecute_NeedsInfo_NoSteeringQueueStaysPaused(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"81": {ID: "81"},
	})
	te.fake.ProgramResult("81", agent.AgentResult{
		Status:    agent.StatusNeedsInfo,
		NeedsInfo: &agent.NeedsInfoDetail{Question: "which config flag?"},
	})
	te.eng.Steering = nil

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "81", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateNeedsInfo {
		t.Fatalf("final state = %s, want NEEDS_INFO (no Steering Queue configured)", result.Issue.State)
	}
	if len(te.fake.Invocations()) != 1 {
		t.Fatalf("got %d agent invocations, want 1 (no resume without a Steering Queue)", len(te.fake.Invocations()))
	}
}
