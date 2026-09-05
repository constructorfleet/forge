package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Teagan42/forge/internal/tui"
)

// liveStore is the read-only store surface the live view needs: the roster
// poller's Execution state plus the transcript feed's runs, tails, and gates.
type liveStore interface {
	tui.RosterStore
	tui.TranscriptFeedStore
}

// runLiveRoster drives the live Bubble Tea roster for executionID until it
// quits. Bubble Tea runs in raw mode and catches panics by default, restoring
// the terminal, so an observer crash cannot leave the shell crosstalk-broken.
// The program takes its own context so the caller can cancel the read loop
// without touching the store. canceller wires the cancel key to the
// in-process operational Engine (ADR 0031); a nil canceller leaves the
// control present but inert, and the key explains itself instead of quietly
// doing nothing.
func runLiveRoster(ctx context.Context, store liveStore, executionID string, canceller tui.Canceller) error {
	roster := tui.NewRoster(store, time.Now)
	model := tui.NewLiveModel(roster, executionID, 0)
	model.SetContext(ctx)
	model.SetFeed(tui.NewTranscriptFeed(store))
	model.Canceller = canceller

	// A quit key removes the diff artifacts itself; this covers every other
	// exit path (cancellation, signal, panic).
	defer model.Close()

	p := tea.NewProgram(model, tea.WithContext(ctx))
	// The caller cancels ctx to stop the observer once the run ends, so a
	// cancellation is the normal exit, not a failure to report.
	if _, err := p.Run(); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("forge: run live roster: %w", err)
	}
	return nil
}
