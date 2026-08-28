package scheduler

import (
	"context"

	"github.com/Teagan42/forge/internal/engine"
)

// engineExecutor bridges *engine.Engine's ExecuteResult-returning Execute
// method to the narrower Executor interface Scheduler depends on, so
// Scheduler's core has no compile-time dependency on internal/engine's
// concrete type. Adapt is the only place in this package that imports
// internal/engine.
type engineExecutor struct {
	eng *engine.Engine
}

// Adapt wraps eng as a scheduler.Executor.
func Adapt(eng *engine.Engine) Executor {
	return engineExecutor{eng: eng}
}

// AdaptCIRepairer wraps eng as a scheduler.CIRepairer.
func AdaptCIRepairer(eng *engine.Engine) CIRepairer {
	return engineExecutor{eng: eng}
}

func (a engineExecutor) Execute(ctx context.Context, issueID, baseRevision string) (ExecuteOutcome, error) {
	result, err := a.eng.Execute(ctx, issueID, baseRevision)
	return ExecuteOutcome{ExecutionID: result.ExecutionID, State: result.Issue.State}, err
}

func (a engineExecutor) Repair(ctx context.Context, executionID, issueID string) (ExecuteOutcome, error) {
	issue, err := a.eng.RepairCIFailure(ctx, executionID, issueID)
	return ExecuteOutcome{ExecutionID: executionID, State: issue.State}, err
}
