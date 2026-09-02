package engine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/gate/gatetest"
)

// TestExecute_QualityGateExecError_RoutesThroughFailOut proves a Quality
// Gate command that cannot run at all (env.Execute returns an error, e.g.
// the container exited while the command was running) is treated as a
// deterministic infrastructure failure — routed through failOut straight to
// a terminal state — rather than folded into an ordinary failing gate.Result
// and handed to the normal repair/retry loop, which cannot succeed against
// a container that is already gone (constructorfleet/forge#391).
func TestExecute_QualityGateExecError_RoutesThroughFailOut(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"91": {ID: "91", Title: "Gate exec error"},
	})
	te.fake.ProgramResult("91", agent.AgentResult{Status: agent.StatusImplemented, Summary: "done"})
	te.eng.Config.Quality.Gates = []config.QualityGate{{Name: "test", Command: "make test"}}

	runner := gatetest.NewFakeCommandRunner()
	runner.ProgramError("make test", errors.New("container: exec: container exited unexpectedly"))
	te.gates.Set(runner)

	ctx := context.Background()
	const executionID = "exec-gate-error"
	te.eng.NewExecutionID = func() string { return executionID }

	if _, err := te.eng.Execute(ctx, "91", te.base); err == nil {
		t.Fatal("Execute: want error when a Quality Gate command fails to run, got nil")
	}

	issue, getErr := te.store.GetIssue(ctx, executionID, "91")
	if getErr != nil {
		t.Fatalf("GetIssue: %v", getErr)
	}
	if issue.State != domain.StateFailed {
		t.Fatalf("issue.State = %s, want FAILED (Exec error must route through failOut, not the repair loop)", issue.State)
	}

	runs, err := te.store.GateRunsByIssue(ctx, executionID, "91")
	if err != nil {
		t.Fatalf("GateRunsByIssue: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("got %d GateRuns, want 0 — an Exec error is infrastructure failure, not a recorded gate run", len(runs))
	}
}
