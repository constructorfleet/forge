package tui

// Package tui/roster.go implements the observation poller that feeds the
// live roster frame: one ~1s pass reads the Store's Execution Issues and
// Worker claims and resolves them into a ViewModel against an injectable
// clock, so the whole state-fetch is deterministic and headless.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
)

// RosterStore is the read-only slice of storage the roster poller needs:
// one Execution's Issues (state, title, state_changed_at, retry budget) and
// the per-Issue Worker claim whose last_heartbeat feeds the liveness badge.
type RosterStore interface {
	LoadExecution(ctx context.Context, executionID string) (storage.ExecutionState, error)
	WorkerClaim(ctx context.Context, executionID, issueID string) (storage.WorkerClaim, error)
	// LatestReviewOutcome supplies the aggregate Review verdict for the detail
	// strip, and tells the frame whether a stored diff exists to page. A poll
	// pass calls it per Issue, so it must not carry the diff body.
	LatestReviewOutcome(ctx context.Context, executionID, issueID string) (storage.ReviewOutcome, error)

	// LatestReviewDiff serves the on-request diff read only (see diff.go). It
	// reads the one diff column, so the pager path loads no finding and no axis
	// envelope.
	LatestReviewDiff(ctx context.Context, executionID, issueID string) (string, error)
}

// Roster fetches an Execution's Worker state into a ViewModel on demand.
// Now is injectable (the LostExecutionController pattern) so a poll pass is
// deterministic under test.
type Roster struct {
	Store RosterStore
	// Now returns the clock each Fetch resolves elapsed and heartbeat age
	// against. Defaults to time.Now when left nil.
	Now func() time.Time
}

// NewRoster builds a Roster over store with the given clock.
func NewRoster(store RosterStore, now func() time.Time) *Roster {
	if now == nil {
		now = time.Now
	}
	return &Roster{Store: store, Now: now}
}

// Fetch performs one poll pass: it reloads executionID's Issues and Worker
// claims and resolves a ViewModel from them. Elapsed comes from
// state_changed_at, heartbeat age from last_heartbeat — distinct quantities,
// as the liveness criterion requires — each against the injected clock.
// A Worker with no claim (planning) claims no liveness.
func (r *Roster) Fetch(ctx context.Context, executionID string, now time.Time) (ViewModel, error) {
	state, err := r.Store.LoadExecution(ctx, executionID)
	if errors.Is(err, storage.ErrNotFound) {
		// The Scheduler writes the Execution after the roster starts polling.
		return ViewModel{Notice: "waiting for the execution to start…"}, nil
	}
	if err != nil {
		return ViewModel{}, fmt.Errorf("tui: load execution %s: %w", executionID, err)
	}

	rows := make([]WorkerRow, 0, len(state.Issues))
	for _, issue := range state.Issues {
		rows = append(rows, r.row(ctx, state.Execution.ID, issue, now))
	}
	vm := ViewModel{Workers: rows}
	if len(rows) == 0 {
		vm.Notice = "no issues in this execution"
	}
	return vm, nil
}

// row resolves one Issue into a WorkerRow. A heartbeat missing or stale is
// displayed, never detected (ADR-0031); a claim read failure other than
// ErrNotFound degrades to a no-heartbeat row rather than aborting the pass.
func (r *Roster) row(ctx context.Context, executionID string, issue domain.Issue, now time.Time) WorkerRow {
	row := WorkerRow{
		IssueID: issue.ID,
		Title:   issue.Title,
		State:   issue.State,
	}
	if !issue.StateChangedAt.IsZero() {
		row.Elapsed = now.Sub(issue.StateChangedAt)
	}
	row.Attempt, row.Budget = attemptBudget(issue.RetryBudget)

	row.Verdict, row.HasDiff = r.lastReview(ctx, executionID, issue.ID)

	claim, err := r.Store.WorkerClaim(ctx, executionID, issue.ID)
	if err == nil && !claim.LastHeartbeat.IsZero() {
		row.HasHeartbeat = true
		row.HeartbeatAge = now.Sub(claim.LastHeartbeat)
	} else if err != nil && !errors.Is(err, storage.ErrNotFound) {
		// A non-"not found" read failure: no heartbeat this pass.
		row.HasHeartbeat = false
	}
	return row
}

// lastReview returns the Issue's current Review verdict plus whether that run
// stored a diff. A read failure degrades to no verdict: the roster is an
// observer and must not abort a pass.
func (r *Roster) lastReview(ctx context.Context, executionID, issueID string) (verdict string, hasDiff bool) {
	out, err := r.Store.LatestReviewOutcome(ctx, executionID, issueID)
	if err != nil || !out.Recorded {
		return "", false
	}
	return out.Verdict, out.HasDiff
}

// attemptBudget derives the frame's "attempt N/B" strip from the retry
// budget: N is 1 plus every recorded failure (the current, 1-based attempt),
// B is 1 plus every ceiling (the total attempts available). A zero budget
// reads as a single initial attempt.
func attemptBudget(b domain.RetryBudget) (attempt, budget int) {
	limits := b.Limits()
	totalLimit := limits.Gate + limits.Review + limits.CI + limits.ProviderLimit
	totalUsed := b.GateFailures() + b.ReviewFailures() + b.CIFailures() + b.ProviderLimitFailures()
	return 1 + totalUsed, 1 + totalLimit
}
