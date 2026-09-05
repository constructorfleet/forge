package engine_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
)

// pausingAgent blocks Execute on resume until the test sends a result,
// letting a test hold RetryIssue inside its resumed workflow so a
// concurrent CancelExecution can be forced to race it.
type pausingAgent struct {
	started chan struct{}
	resume  chan agent.AgentResult
	once    sync.Once
}

func newPausingAgent() *pausingAgent {
	return &pausingAgent{started: make(chan struct{}), resume: make(chan agent.AgentResult)}
}

func (a *pausingAgent) Execute(ctx context.Context, _ agent.AgentRequest) (agent.AgentResult, error) {
	a.once.Do(func() { close(a.started) })
	select {
	case res := <-a.resume:
		return res, nil
	case <-ctx.Done():
		return agent.AgentResult{Status: agent.StatusFailed, Summary: "cancelled"}, ctx.Err()
	}
}

// TestCancelExecution_WaitsForConcurrentRetryToRelease drives the race issue
// 552 names: a cancel that starts while RetryIssue is mid-flight, between
// Store.ClaimRetry and resumeIssue re-claiming the Issue. Before the shared
// IssueLock, CancelExecution could land in that window and finish "cancelled"
// while resumeIssue went on to start a Worker anyway. With the lock,
// CancelExecution must wait for the in-flight retry to finish before it can
// act, so the two commands never interleave.
func TestCancelExecution_WaitsForConcurrentRetryToRelease(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"552": {ID: "552", Title: "Racing cancel and retry"},
	})
	executionID := runToFailed(t, te, "552")

	pausing := newPausingAgent()
	te.eng.Agent = pausing

	retryDone := make(chan error, 1)
	go func() {
		_, err := te.eng.RetryIssue(context.Background(), executionID, "552")
		retryDone <- err
	}()

	select {
	case <-pausing.started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the retry to reach the agent step")
	}

	cancelDone := make(chan error, 1)
	go func() {
		_, err := te.eng.CancelExecution(context.Background(), executionID)
		cancelDone <- err
	}()

	select {
	case <-cancelDone:
		t.Fatal("CancelExecution returned while the concurrent retry still held the issue lock")
	case <-time.After(200 * time.Millisecond):
	}

	pausing.resume <- agent.AgentResult{Status: agent.StatusImplemented, Summary: "fixed"}

	if err := <-retryDone; err != nil {
		t.Fatalf("RetryIssue: %v", err)
	}
	if err := <-cancelDone; err != nil {
		t.Fatalf("CancelExecution: %v", err)
	}

	issue, err := te.store.GetIssue(context.Background(), executionID, "552")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.State != domain.StateCancelled {
		t.Fatalf("final state = %s, want CANCELLED", issue.State)
	}
}
