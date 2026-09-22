package tui

// Package tui/roster.go implements the observation poller that feeds the
// live roster frame: one ~1s pass reads the Store's Execution Issues and
// Worker claims and resolves them into a ViewModel against an injectable
// clock, so the whole state-fetch is deterministic and headless.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
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
	// LatestReviewVerdicts supplies the aggregate Review verdict for every
	// Issue in the Execution, keyed by IssueID, and tells the frame whether
	// each carries a stored diff to page. A poll pass calls it once per pass
	// rather than once per Issue, and it must not carry any diff body.
	LatestReviewVerdicts(ctx context.Context, executionID string) (map[string]storage.ReviewOutcome, error)

	// LatestReviewDiff serves the on-request diff read only (see diff.go). It
	// reads the one diff column, so the pager path loads no finding and no axis
	// envelope.
	LatestReviewDiff(ctx context.Context, executionID, issueID string) (string, error)

	// GetReplanCheckpoint serves the on-request replan-artifact read only (see
	// approve.go). It is the record the approve key defers to $PAGER.
	GetReplanCheckpoint(ctx context.Context, executionID, issueID string) (storage.ReplanCheckpoint, error)

	// GetNeedsInfoCheckpoint serves the on-request needs-info question read
	// only (see answer.go). It is the record the answer key defers to $EDITOR.
	GetNeedsInfoCheckpoint(ctx context.Context, executionID, issueID string) (storage.NeedsInfoCheckpoint, error)

	// TranscriptAgents supplies one summary per AgentRun for the Issue, so the
	// roster lists started agents and their latest output without reading any
	// event body.
	TranscriptAgents(ctx context.Context, executionID, issueID string) ([]storage.TranscriptAgent, error)

	// AgentRunsByIssue supplies the Issue's recorded attempts, so the roster's
	// "attempt N" count derives from the same source the transcript pane's
	// own "── attempt N ──" divider numbers from (see attemptBudget). It must
	// not carry a run's transcript, so a poll pass reads only the row.
	AgentRunsByIssue(ctx context.Context, executionID, issueID string) ([]storage.AgentRun, error)
}

// LiveExecutionLister discovers executions that have a current Worker claim.
// It is optional so narrow roster test doubles and older stores remain valid.
type LiveExecutionLister interface {
	LiveWorkerExecutionIDs(ctx context.Context, cutoff time.Time) ([]string, error)
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
	return r.FetchMany(ctx, []string{executionID}, now)
}

// FetchLive discovers and merges every execution with a live Worker heartbeat.
func (r *Roster) FetchLive(ctx context.Context, now time.Time) (ViewModel, error) {
	lister, ok := r.Store.(LiveExecutionLister)
	if !ok {
		return ViewModel{}, fmt.Errorf("tui: store cannot list live executions")
	}
	ids, err := lister.LiveWorkerExecutionIDs(ctx, now.Add(-StaleHeartbeat))
	if err != nil {
		return ViewModel{}, fmt.Errorf("tui: list live executions: %w", err)
	}
	sortedIDs := append([]string(nil), ids...)
	sort.Strings(sortedIDs)
	return r.FetchMany(ctx, sortedIDs, now)
}

// FetchMany merges the Issues from several Executions into one roster.
func (r *Roster) FetchMany(ctx context.Context, executionIDs []string, now time.Time) (ViewModel, error) {
	vm := ViewModel{}
	executionIDs = stableExecutionIDs(executionIDs)
	loadedRequested := false
	requestedNotFound := false
	var failures []string
	for _, executionID := range executionIDs {
		state, err := r.Store.LoadExecution(ctx, executionID)
		if errors.Is(err, storage.ErrNotFound) {
			if len(executionIDs) == 1 {
				requestedNotFound = true
			}
			continue
		}
		if err != nil {
			if len(executionIDs) == 1 {
				return ViewModel{}, fmt.Errorf("tui: load execution %s: %w", executionID, err)
			}
			failures = append(failures, fmt.Sprintf("%s: %v", executionID, err))
			continue
		}
		vm.ExecutionIDs = append(vm.ExecutionIDs, state.Execution.ID)
		if len(executionIDs) == 1 {
			loadedRequested = true
		}
		verdicts, err := r.Store.LatestReviewVerdicts(ctx, state.Execution.ID)
		if err != nil {
			verdicts = nil
		}
		for _, issue := range state.Issues {
			vm.Workers = append(vm.Workers, r.row(ctx, state.Execution.ID, issue, now, verdicts))
		}
	}
	if len(vm.Workers) == 0 {
		if len(executionIDs) == 1 {
			if requestedNotFound && !loadedRequested {
				vm.Notice = "waiting for the execution to start…"
			} else {
				vm.Notice = "no issues in this execution"
			}
		} else {
			vm.Notice = "no live executions"
		}
	}
	if len(vm.ExecutionIDs) == 1 {
		vm.ExecutionID = vm.ExecutionIDs[0]
	}
	sort.SliceStable(vm.Workers, func(i, j int) bool {
		if vm.Workers[i].ExecutionID != vm.Workers[j].ExecutionID {
			return vm.Workers[i].ExecutionID < vm.Workers[j].ExecutionID
		}
		return vm.Workers[i].IssueID < vm.Workers[j].IssueID
	})
	if len(failures) > 0 {
		failureNotice := "failed to load executions: " + strings.Join(failures, "; ")
		if vm.Notice != "" {
			vm.Notice += "; " + failureNotice
		} else {
			vm.Notice = failureNotice
		}
	}
	return vm, nil
}

func stableExecutionIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	out := sorted[:0]
	for _, id := range sorted {
		if id == "" || (len(out) > 0 && out[len(out)-1] == id) {
			continue
		}
		out = append(out, id)
	}
	return out
}

// row resolves one Issue into a WorkerRow. A heartbeat missing or stale is
// displayed, never detected (ADR-0031); a claim read failure other than
// ErrNotFound degrades to a no-heartbeat row rather than aborting the pass.
func (r *Roster) row(ctx context.Context, executionID string, issue domain.Issue, now time.Time, verdicts map[string]storage.ReviewOutcome) WorkerRow {
	row := WorkerRow{
		ExecutionID: executionID,
		IssueID:     issue.ID,
		Title:       issue.Title,
		State:       issue.State,
	}
	if !issue.StateChangedAt.IsZero() {
		row.Elapsed = now.Sub(issue.StateChangedAt)
	}
	runs, err := r.Store.AgentRunsByIssue(ctx, executionID, issue.ID)
	if err != nil {
		// A read failure degrades to the initial-attempt floor: the roster is
		// an observer and must not abort a pass over one failed count.
		runs = nil
	}
	row.Attempt, row.Budget = attemptBudget(len(runs), issue.RetryBudget)

	row.Verdict, row.HasDiff = lastReview(verdicts, issue.ID)

	agents, err := r.Store.TranscriptAgents(ctx, executionID, issue.ID)
	if err != nil {
		// A read failure degrades to the implementation row alone: the roster
		// is an observer and must not abort a pass over one failed summary.
		agents = nil
	}
	row.Agents = DeriveAgents(agents)

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

// lastReview looks up issueID's current Review verdict plus whether that run
// stored a diff, from the whole-Execution map Fetch already read. An Issue
// absent from verdicts (no recorded run, or the aggregate read failed) reports
// no verdict.
func lastReview(verdicts map[string]storage.ReviewOutcome, issueID string) (verdict string, hasDiff bool) {
	out, ok := verdicts[issueID]
	if !ok {
		return "", false
	}
	return out.Verdict, out.HasDiff
}

// attemptBudget derives the frame's "attempt N/B" strip: N is the Issue's
// recorded AgentRun count (runCount), floored at one so an Issue with no
// recorded run yet still reads as its initial attempt; B is 1 plus every
// retry-budget ceiling (the total attempts available). N shares runCount with
// the transcript pane's own "── attempt N ──" divider (see pane.go's
// attemptNumbers), which numbers the same AgentRuns in the same insertion
// order: a repair that restarts the Agent without recording a gate, review,
// CI, or provider-limit failure — a lost-execution recovery restart, for
// instance — still advances N, so the two views cannot drift apart the way a
// failure-tally count could.
func attemptBudget(runCount int, b domain.RetryBudget) (attempt, budget int) {
	attempt = runCount
	if attempt < 1 {
		attempt = 1
	}
	limits := b.Limits()
	totalLimit := limits.Gate + limits.Review + limits.CI + limits.ProviderLimit
	return attempt, 1 + totalLimit
}
