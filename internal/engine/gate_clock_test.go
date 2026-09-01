package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/gate/gatetest"
)

// TestExecute_GateRunTimestampsUseEngineClock proves each recorded GateRun's
// StartedAt/FinishedAt come from Engine.Now, the same injectable seam every
// other Engine timestamp uses, rather than the wall clock. Without this, a
// fast in-memory gate command can start and finish within the same real
// time.Now() tick, so the persisted GateRun's Duration is not deterministic
// (constructorfleet/forge#327).
func TestExecute_GateRunTimestampsUseEngineClock(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"77": {ID: "77", Title: "Gate clock"},
	})
	te.fake.ProgramResult("77", agent.AgentResult{Status: agent.StatusImplemented, Summary: "done"})
	te.eng.Config.Quality.Gates = []config.QualityGate{{Name: "test", Command: "make test"}}

	runner := gatetest.NewFakeCommandRunner()
	runner.ProgramResult("make test", 0, "ok", "")
	te.gates.Set(runner)

	// A generous, strictly increasing sequence covers every Now() call
	// Execute makes along the way (transitions, workspace creation, the
	// agent run, and the gate itself), so whichever calls land on the gate
	// still observe two distinct, ordered timestamps.
	base := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	times := make([]time.Time, 200)
	for i := range times {
		times[i] = base.Add(time.Duration(i) * time.Second)
	}
	te.eng.Now = steppedClock(times...)

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "77", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	runs, err := te.store.GateRunsByIssue(ctx, result.ExecutionID, "77")
	if err != nil {
		t.Fatalf("GateRunsByIssue: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d GateRuns, want 1", len(runs))
	}
	run := runs[0]

	// Every value the injected clock hands out falls on an exact second
	// within the base..base+200s window. A GateRun timestamp sourced from
	// the real wall clock instead would not land on that grid.
	inWindow := func(ts time.Time) bool {
		return !ts.Before(base) && ts.Before(base.Add(time.Duration(len(times))*time.Second)) && ts.Sub(base)%time.Second == 0
	}
	if !inWindow(run.StartedAt) {
		t.Fatalf("GateRun.StartedAt = %v, want a value from the injected clock", run.StartedAt)
	}
	if !inWindow(run.FinishedAt) {
		t.Fatalf("GateRun.FinishedAt = %v, want a value from the injected clock", run.FinishedAt)
	}
	if !run.FinishedAt.After(run.StartedAt) {
		t.Fatalf("GateRun.FinishedAt = %v, want strictly after StartedAt %v", run.FinishedAt, run.StartedAt)
	}
}
