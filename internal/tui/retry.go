package tui

// retry.go issues the one out-of-process control (ADR 0031): RetryIssue ends
// in resumeIssue — full re-entry into workspace setup, rebase, the coding
// agent, the repair loop, gates, commit, and PR, the very orchestrator this
// TUI observes — so an in-process call would re-enter it. Retry instead
// spawns a detached forge child (ProcessRetrier) that keeps running after the
// TUI quits. Unlike cancel, retry needs no confirmation: it is not a
// store-mutating call in this process.

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// Retrier is the narrow seam the retry key calls into. The production
// implementation, ProcessRetrier, spawns a detached forge child; tests inject
// a fake so no test spawns a real process.
type Retrier interface {
	Retry(executionID, issueID string) (RetryResult, error)
}

// RetryResult carries a finished detached retry child's outcome. Stderr is
// captured (issue #458: some refreshRetryBase failures leave no trace in the
// store) so a refused or failing retry is diagnosable from the child's own
// output, not only from the store.
type RetryResult struct {
	Stderr   string
	ExitCode int
}

// startRetry returns the command that spawns a detached retry child off the
// update goroutine, so a slow spawn cannot delay a key press. It marks the
// call in flight, so a second retry key press cannot double-issue it while
// this one still runs; that guard covers only this model's own call, per the
// control seam's concurrency rule.
func (m *LiveModel) startRetry() tea.Cmd {
	row, ok := selectedWorker(m.vm)
	if !ok || !IsRetryLegal(row.State) {
		m.vm.ActionNotice = "no retryable Worker selected"
		return nil
	}
	if m.retrying {
		m.vm.ActionNotice = "retry already in flight"
		return nil
	}
	if m.Retrier == nil {
		m.vm.ActionNotice = "retry is not available"
		return nil
	}
	m.retrying = true
	m.vm.ActionNotice = fmt.Sprintf("retrying issue %s…", row.IssueID)
	retrier, executionID, issueID := m.Retrier, m.ExecutionID, row.IssueID
	return func() tea.Msg {
		result, err := retrier.Retry(executionID, issueID)
		return retryResultMsg{issueID: issueID, result: result, err: err}
	}
}

// retryResultMsg carries a finished Retry call back to the update loop.
type retryResultMsg struct {
	issueID string
	result  RetryResult
	err     error
}

// applyRetryResult commits a finished retry call. A failing spawn or a
// non-zero exit is surfaced, never swallowed, so the operator can tell a
// refused or failing retry apart from one never attempted (ActionNotice is
// empty until a control sets it).
func (m *LiveModel) applyRetryResult(msg retryResultMsg) {
	m.retrying = false
	if msg.err != nil {
		m.vm.ActionNotice = fmt.Sprintf("retry %s: %v", msg.issueID, msg.err)
		return
	}
	m.vm.ActionNotice = fmt.Sprintf("retry requested for %s", msg.issueID)
}
