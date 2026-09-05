package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
)

// TestResumeExecution_WaitsForConcurrentRetryToRelease drives the race issue
// 684 names: RetryIssue's own doc comment notes that Store.ClaimRetry
// publishes the Issue as READY with no Worker claim for the duration of the
// base refresh, and a concurrent `forge resume` reads that shape as
// resumable. Issue 552 only serialized CancelExecution against RetryIssue
// through the shared IssueLock; ResumeExecution did not take that lock and
// could still land in the same window and start a second Worker in the same
// Workspace. With ResumeExecution also taking IssueLock per Issue, it must
// wait for the in-flight retry to finish before it can act.
func TestResumeExecution_WaitsForConcurrentRetryToRelease(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"684": {ID: "684", Title: "Racing resume and retry"},
	})
	executionID := runToFailed(t, te, "684")

	pausing := newPausingAgent()
	te.eng.Agent = pausing

	retryDone := make(chan error, 1)
	go func() {
		_, err := te.eng.RetryIssue(context.Background(), executionID, "684")
		retryDone <- err
	}()

	select {
	case <-pausing.started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the retry to reach the agent step")
	}

	resumeDone := make(chan error, 1)
	go func() {
		_, err := te.eng.ResumeExecution(context.Background(), executionID)
		resumeDone <- err
	}()

	select {
	case <-resumeDone:
		t.Fatal("ResumeExecution returned while the concurrent retry still held the issue lock")
	case <-time.After(200 * time.Millisecond):
	}

	pausing.resume <- agent.AgentResult{Status: agent.StatusImplemented, Summary: "fixed"}

	if err := <-retryDone; err != nil {
		t.Fatalf("RetryIssue: %v", err)
	}
	if err := <-resumeDone; err != nil {
		t.Fatalf("ResumeExecution: %v", err)
	}
}
