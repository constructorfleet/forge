package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Teagan42/forge/internal/tui"
)

// runLiveRoster drives the live Bubble Tea roster for executionID until it
// quits. Bubble Tea runs in raw mode and catches panics by default, restoring
// the terminal, so an observer crash cannot leave the shell crosstalk-broken.
// The program takes its own context so the caller can cancel the read loop
// without touching the store.
func runLiveRoster(ctx context.Context, store tui.RosterStore, executionID string) error {
	roster := tui.NewRoster(store, time.Now)
	model := tui.NewLiveModel(roster, executionID, 0)
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
