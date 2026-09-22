package tui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Teagan42/forge/internal/storage"
)

// ObservationLister is the small read surface used by the TUI observer.
// Implementations must not expose the full storage.Store interface.
type ObservationLister interface {
	LoadExecution(context.Context, string) (storage.ExecutionState, error)
	LiveRuns(context.Context) ([]storage.LiveRun, error)
	TranscriptEventsAfter(context.Context, int64, int64, int64) ([]storage.TranscriptEvent, error)
}

// SchemaVerifier checks migrations without applying them.
type SchemaVerifier interface{ VerifySchema(context.Context) error }

// Observation is one consistent store-poll result. Events contains only new
// events from this pass. A gap means the live view missed retained events.
type Observation struct {
	State   storage.ExecutionState
	Runs    []storage.LiveRun
	Events  map[int64][]storage.TranscriptEvent
	Gaps    map[int64]bool
	cursors map[int64]int64
}

// Cursor returns the last sequence consumed for a run, or -1 before attach.
func (o Observation) Cursor(runID int64) int64 {
	if cursor, ok := o.cursors[runID]; ok {
		return cursor
	}
	return -1
}

// Poller owns the TUI observation loop. It never writes engineering state.
type Poller struct {
	Store         ObservationLister
	Now           func() time.Time
	Sleep         func(context.Context, time.Duration) error
	cursors       map[int64]int64
	schemaChecked bool
}

// ObservationPoller is the descriptive name for Poller.
type ObservationPoller = Poller

// TUIObservationController is retained as an explicit seam name for callers.
type TUIObservationController = Poller

// NewObservationPoller creates a TUI observer with injectable time functions.
func NewObservationPoller(store ObservationLister, now func() time.Time) *Poller {
	if now == nil {
		now = time.Now
	}
	return &Poller{Store: store, Now: now, cursors: make(map[int64]int64), Sleep: sleepContext}
}

// NewPoller is a short constructor alias.
func NewPoller(store ObservationLister, now func() time.Time) *Poller {
	return NewObservationPoller(store, now)
}

// Pass reads execution state and all live transcript tails. One failed run
// does not stop the other runs; failures are returned with errors.Join.
func (p *Poller) Pass(ctx context.Context, executionID string) (Observation, error) {
	result := Observation{Events: make(map[int64][]storage.TranscriptEvent), Gaps: make(map[int64]bool), cursors: make(map[int64]int64)}
	var errs []error
	if !p.schemaChecked {
		if verifier, ok := p.Store.(SchemaVerifier); ok {
			if err := verifier.VerifySchema(ctx); err != nil {
				return result, fmt.Errorf("tui: verify schema: %w", err)
			}
		}
		p.schemaChecked = true
	}
	if executionID != "" {
		state, err := p.Store.LoadExecution(ctx, executionID)
		if err != nil {
			errs = append(errs, fmt.Errorf("tui: load execution %s: %w", executionID, err))
		} else {
			result.State = state
		}
	}
	runs, err := p.Store.LiveRuns(ctx)
	if err != nil {
		return result, errors.Join(append(errs, fmt.Errorf("tui: list live runs: %w", err))...)
	}
	for _, run := range runs {
		if executionID != "" && run.ExecutionID != executionID {
			continue
		}
		result.Runs = append(result.Runs, run)
		after, ok := p.cursors[run.AgentRunID]
		if !ok {
			after = -1
		}
		previous := after
		events, readErr := p.Store.TranscriptEventsAfter(ctx, run.AgentRunID, after, transcriptPollLimit)
		if readErr != nil {
			errs = append(errs, fmt.Errorf("tui: read transcript run %d: %w", run.AgentRunID, readErr))
			result.cursors[run.AgentRunID] = after
			continue
		}
		if len(events) > 0 {
			result.Events[run.AgentRunID] = events
			for _, event := range events {
				if int64(event.Seq) > after {
					after = int64(event.Seq)
				}
			}
			if int64(events[0].Seq) > previous+1 {
				result.Gaps[run.AgentRunID] = true
			}
		}
		p.cursors[run.AgentRunID] = after
		result.cursors[run.AgentRunID] = after
	}
	sort.Slice(result.Runs, func(i, j int) bool { return result.Runs[i].AgentRunID < result.Runs[j].AgentRunID })
	return result, errors.Join(errs...)
}

// Run repeats Pass until the context ends. A failed pass is reported and does
// not stop later passes.
func (p *Poller) Run(ctx context.Context, executionID string, interval time.Duration, onErr func(error)) error {
	for {
		if _, err := p.Pass(ctx, executionID); err != nil && onErr != nil {
			onErr(err)
		}
		if err := p.Sleep(ctx, interval); err != nil {
			return err
		}
	}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
