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

type liveControls struct {
	canceller tui.Canceller
	retrier   tui.Retrier
	resumer   tui.Resumer
	approver  tui.Approver
	answerer  tui.Answerer
}

// runLiveRoster drives the live Bubble Tea roster for executionID, or discovers
// every live Execution when executionID is empty, until it
// quits. Bubble Tea runs in raw mode and catches panics by default, restoring
// the terminal, so an observer crash cannot leave the shell crosstalk-broken.
// An empty executionID enables live discovery on every poll. A non-empty ID
// pins the roster to that Execution. The program takes its own context.
// The caller can cancel the read loop without touching the store. Canceller
// wires cancel and approver wires approve to the in-process operational Engine
// (ADR 0031). A nil seam leaves its control inert, and the key explains itself.
// Retrier and resumer wire retry and resume to detached forge children. A nil
// seam leaves that control inert too. Answerer wires the answer key to a
// Tracker's AddComment (issue #505); a nil answerer — from
// resolveAnswerer's up-front auth preflight failing — leaves the answer
// control present but inert, with no offline fallback.
func runLiveRoster(ctx context.Context, store liveStore, executionID string, controls liveControls) error {
	roster := tui.NewRoster(store, time.Now)
	model := tui.NewLiveModel(roster, executionID, 0)
	model.SetContext(ctx)
	model.SetFeed(tui.NewTranscriptFeed(store))
	model.Canceller = controls.canceller
	model.Retrier = controls.retrier
	model.Resumer = controls.resumer
	model.Approver = controls.approver
	model.Answerer = controls.answerer

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
