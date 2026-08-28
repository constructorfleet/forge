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

// StatusStore is the subset of storage.Store LoadStatus needs: reloading an
// Execution's Issues and its Event log. A narrower interface than
// storage.Store so callers (e.g. `forge status`) don't need to construct a
// partial Engine — with its Tracker/Workspaces/Agent fields left zero —
// just to reach a pure read.
type StatusStore interface {
	LoadExecution(ctx context.Context, executionID string) (storage.ExecutionState, error)
	EventsByExecution(ctx context.Context, executionID string) ([]storage.Event, error)
}

// LoadStatus reloads a StatusReport for executionID from store. It performs
// no orchestration of its own: `forge status` is a pure read over whatever
// Execute already persisted.
func LoadStatus(ctx context.Context, store StatusStore, executionID string) (StatusReport, error) {
	state, err := store.LoadExecution(ctx, executionID)
	if err != nil {
		return StatusReport{}, fmt.Errorf("engine: load execution %s: %w", executionID, err)
	}
	events, err := store.EventsByExecution(ctx, executionID)
	if err != nil {
		return StatusReport{}, fmt.Errorf("engine: load events for execution %s: %w", executionID, err)
	}
	return StatusReport{Execution: state.Execution, Issues: state.Issues, Events: events}, nil
}
