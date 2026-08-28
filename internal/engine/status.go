package engine

import (
	"context"
	"fmt"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
)

// StatusReport is the persisted state of one Execution: the Execution
// itself, every Issue recorded against it, and its full Event log — enough
// to answer "what happened" without replaying anything.
type StatusReport struct {
	Execution domain.Execution
	Issues    []domain.Issue
	Events    []storage.Event
}

// Status reloads a StatusReport for executionID from Store. It performs no
// orchestration of its own: `forge status` is a pure read over what Execute
// already persisted.
func (e *Engine) Status(ctx context.Context, executionID string) (StatusReport, error) {
	state, err := e.Store.LoadExecution(ctx, executionID)
	if err != nil {
		return StatusReport{}, fmt.Errorf("engine: load execution %s: %w", executionID, err)
	}
	events, err := e.Store.EventsByExecution(ctx, executionID)
	if err != nil {
		return StatusReport{}, fmt.Errorf("engine: load events for execution %s: %w", executionID, err)
	}
	return StatusReport{Execution: state.Execution, Issues: state.Issues, Events: events}, nil
}
