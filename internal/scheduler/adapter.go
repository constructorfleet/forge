package scheduler

import (
	"context"
	"sync"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
)

// engineExecutor bridges *engine.Engine's ExecuteResult-returning Execute
// method to the narrower Executor interface Scheduler depends on, so
// Scheduler's core has no compile-time dependency on internal/engine's
// concrete type. Adapt is the only place in this package that imports
// internal/engine.
type engineExecutor struct {
	eng *engine.Engine

	mu        sync.Mutex
	execution *domain.Execution
}

// Adapt wraps eng as a scheduler.Executor.
func Adapt(eng *engine.Engine) Executor {
	return &engineExecutor{eng: eng}
}

// AdaptCIRepairer wraps eng as a scheduler.CIRepairer.
func AdaptCIRepairer(eng *engine.Engine) CIRepairer {
	return &engineExecutor{eng: eng}
}

func (a *engineExecutor) Execute(ctx context.Context, issueID, baseRevision string) (ExecuteOutcome, error) {
	execution, err := a.ensureExecution(ctx, baseRevision)
	if err != nil {
		return ExecuteOutcome{}, err
	}
	result, err := a.eng.ExecuteInExecution(ctx, execution, issueID, baseRevision)
	return ExecuteOutcome{ExecutionID: result.ExecutionID, State: result.Issue.State}, err
}

func (a *engineExecutor) Repair(ctx context.Context, executionID, issueID string) (ExecuteOutcome, error) {
	issue, err := a.eng.RepairCIFailure(ctx, executionID, issueID)
	return ExecuteOutcome{ExecutionID: executionID, State: issue.State}, err
}

func (a *engineExecutor) StartRun(ctx context.Context, executionBase string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	execution, err := a.eng.StartExecution(ctx, executionBase)
	if err != nil {
		return err
	}
	a.execution = &execution
	return nil
}

func (a *engineExecutor) FinishRun() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.execution = nil
}

func (a *engineExecutor) ensureExecution(ctx context.Context, baseRevision string) (domain.Execution, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.execution != nil {
		return *a.execution, nil
	}
	execution, err := a.eng.StartExecution(ctx, baseRevision)
	if err != nil {
		return domain.Execution{}, err
	}
	a.execution = &execution
	return execution, nil
}
