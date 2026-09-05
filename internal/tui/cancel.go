package tui

// cancel.go issues the one in-process, store-mutating control (ADR 0031):
// cancel runs CancelExecution on an operational Engine (store writes + PID
// syscalls). Confirmation is UI-only frame state, never a store write, and
// the action is pending-until-observed: the model never mutates its own
// polled rows, it waits for the next roster read to show CANCELLED.

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Teagan42/forge/internal/storage"
)

// Canceller is the narrow seam the cancel key calls into. *engine.Engine
// satisfies it; the TUI depends on no wider Engine surface.
type Canceller interface {
	CancelExecution(ctx context.Context, executionID string) (storage.ExecutionState, error)
}

// armCancelConfirm arms the UI-only confirmation for the cancel key. It fires
// no store write: it only sets frame state, so a stray key press mutates
// nothing.
func (m *LiveModel) armCancelConfirm() tea.Cmd {
	if m.cancelling {
		m.vm.ActionNotice = "cancel already in flight"
		return nil
	}
	row, ok := selectedWorker(m.vm)
	if !ok || !IsCancelLegal(row.State) {
		m.vm.ActionNotice = "no cancellable Worker selected"
		return nil
	}
	m.confirming = true
	m.vm.ActionNotice = fmt.Sprintf("cancel execution %s? [y] confirm, any other key cancels", m.ExecutionID)
	return nil
}

// resolveCancelConfirm answers the armed confirmation: y fires the cancel,
// any other key abandons it. It is the only place confirming is cleared, so
// the model never fires a second cancel from a stray key.
func (m *LiveModel) resolveCancelConfirm(key uv.Key) tea.Cmd {
	m.confirming = false
	if !key.MatchString("y") {
		m.vm.ActionNotice = "cancel declined"
		return nil
	}
	return m.startCancel()
}

// startCancel returns the command that runs CancelExecution off the update
// goroutine, so a slow cancel cannot delay a key press. It marks the call in
// flight, so a second cancel key press cannot double-issue it while this one
// still runs; that guard covers only this model's own call, per the control
// seam's concurrency rule — a racing actor's own cancel is not prevented,
// only surfaced when it fails.
func (m *LiveModel) startCancel() tea.Cmd {
	if m.Canceller == nil {
		m.vm.ActionNotice = "cancel is not available"
		return nil
	}
	m.cancelling = true
	m.vm.ActionNotice = fmt.Sprintf("cancelling execution %s…", m.ExecutionID)
	canceller, ctx, executionID := m.Canceller, m.ctx, m.ExecutionID
	return func() tea.Msg {
		_, err := canceller.CancelExecution(ctx, executionID)
		return cancelResultMsg{err: err}
	}
}

// cancelResultMsg carries a finished CancelExecution call back to the update
// loop. Pending-until-observed means this message never mutates a row's
// state itself: the next roster poll is what shows CANCELLED.
type cancelResultMsg struct{ err error }

// applyCancelResult commits a finished cancel call. A CancelOwnerError is a
// warning the cancel still completed; any other error is surfaced as a
// failure. Neither is swallowed, and neither retries automatically: the
// spec is explicit that a failing or racing cancel surfaces the failure.
func (m *LiveModel) applyCancelResult(msg cancelResultMsg) {
	m.cancelling = false
	if msg.err == nil {
		m.vm.ActionNotice = fmt.Sprintf("cancel requested for %s", m.ExecutionID)
		return
	}
	m.vm.ActionNotice = "cancel: " + msg.err.Error()
}
