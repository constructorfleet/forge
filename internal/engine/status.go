package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
)

type TelemetrySummary struct {
	IssuesCompleted   int
	AgentInvocations  int
	InputTokens       int
	OutputTokens      int
	GateRetries       int
	ReviewRetries     int
	CIRetries         int
	ContextBytes      int
	WallClockDuration time.Duration
}

type IssueTelemetry struct {
	IssueID          string
	AgentInvocations int
	InputTokens      int
	OutputTokens     int
	GateRetries      int
	ReviewRetries    int
	CIRetries        int
	ContextBytes     int
	CycleTime        time.Duration
}

type StructuredEvent struct {
	OccurredAt   time.Time
	ExecutionID  string
	IssueID      string
	WorkerID     string
	State        domain.IssueState
	Event        string
	Duration     time.Duration
	AgentBackend string
}

type ExecutionSummary struct {
	Execution       domain.Execution
	IssueCount      int
	ActiveIssues    int
	DoneIssues      int
	FailedIssues    int
	CancelledIssues int
}

type IssueStatus struct {
	Issue          domain.Issue
	WorkerRef      string
	PullRequestURL string
	Failure        string
	Dependencies   []string
}

type TelemetryReport struct {
	Summary TelemetrySummary
	Issues  []IssueTelemetry
	Events  []StructuredEvent
}

// StatusReport is the persisted state of one Execution: the Execution
// itself, every Issue recorded against it, and its full Event log — enough
// to answer "what happened" without replaying anything.
type StatusReport struct {
	Execution domain.Execution
	Issues    []IssueStatus
	Telemetry TelemetryReport
	Events    []storage.Event
}

// StatusStore is the subset of storage.Store LoadStatus needs: reloading an
// Execution's Issues and its Event log. A narrower interface than
// storage.Store so callers (e.g. `forge status`) don't need to construct a
// partial Engine — with its Tracker/Workspaces/Agent fields left zero —
// just to reach a pure read.
type StatusStore interface {
	ListExecutions(ctx context.Context) ([]storage.ExecutionState, error)
	LoadExecution(ctx context.Context, executionID string) (storage.ExecutionState, error)
	EventsByExecution(ctx context.Context, executionID string) ([]storage.Event, error)
	AgentRunsByExecution(ctx context.Context, executionID string) ([]storage.AgentRun, error)
	GateRunsByIssue(ctx context.Context, executionID, issueID string) ([]storage.GateRun, error)
	ReviewRunsByIssueWithoutDiff(ctx context.Context, executionID, issueID string) ([]storage.ReviewRun, error)
	CIRunsByIssue(ctx context.Context, executionID, issueID string) ([]storage.CIRun, error)
	WorkerClaim(ctx context.Context, executionID, issueID string) (storage.WorkerClaim, error)
	PullRequestsByIssue(ctx context.Context, executionID, issueID string) ([]storage.PullRequest, error)
	TranscriptEventsByIssue(ctx context.Context, executionID, issueID string) ([]storage.TranscriptEvent, error)
}

func ListActiveExecutions(ctx context.Context, store StatusStore) ([]ExecutionSummary, error) {
	states, err := store.ListExecutions(ctx)
	if err != nil {
		return nil, fmt.Errorf("engine: list executions: %w", err)
	}

	summaries := make([]ExecutionSummary, 0, len(states))
	for _, state := range states {
		summary := ExecutionSummary{
			Execution:  state.Execution,
			IssueCount: len(state.Issues),
		}
		for _, issue := range state.Issues {
			// Coarse bucketing is the canonical issueState.Group(); only the
			// DONE/CANCELLED split inside GroupDone stays on the exact state
			// because this report reports them separately.
			switch issue.State.Group() {
			case domain.GroupFailed:
				summary.FailedIssues++
			case domain.GroupDone:
				if issue.State == domain.StateCancelled {
					summary.CancelledIssues++
				} else {
					summary.DoneIssues++
				}
			default:
				summary.ActiveIssues++
			}
		}
		if summary.ActiveIssues > 0 {
			summaries = append(summaries, summary)
		}
	}
	return summaries, nil
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
	telemetry, err := buildTelemetry(ctx, store, state, events)
	if err != nil {
		return StatusReport{}, err
	}
	issues, err := buildIssueStatuses(ctx, store, state, events)
	if err != nil {
		return StatusReport{}, err
	}
	return StatusReport{Execution: state.Execution, Issues: issues, Telemetry: telemetry, Events: events}, nil
}

// LoadTranscript reloads every TranscriptEvent recorded for one Issue
// (ticket 28's read surface), across every AgentRun attempt, in
// chronological order — a pure read over whatever the Agent Adapter's
// best-effort capture already persisted, exactly as LoadStatus is a pure
// read over the rest of an Execution's state.
func LoadTranscript(ctx context.Context, store StatusStore, executionID, issueID string) ([]storage.TranscriptEvent, error) {
	events, err := store.TranscriptEventsByIssue(ctx, executionID, issueID)
	if err != nil {
		return nil, fmt.Errorf("engine: load transcript for issue %s/%s: %w", executionID, issueID, err)
	}
	return events, nil
}

func buildIssueStatuses(ctx context.Context, store StatusStore, state storage.ExecutionState, events []storage.Event) ([]IssueStatus, error) {
	statuses := make([]IssueStatus, 0, len(state.Issues))
	for _, issue := range state.Issues {
		status := IssueStatus{
			Issue: issue,
		}
		for _, dep := range issue.Dependencies {
			status.Dependencies = append(status.Dependencies, dep.DependsOnID)
		}

		claim, err := store.WorkerClaim(ctx, state.Execution.ID, issue.ID)
		switch {
		case err == nil:
			status.WorkerRef = claim.WorkerRef
		case !errors.Is(err, storage.ErrNotFound):
			return nil, fmt.Errorf("engine: load worker for issue %s: %w", issue.ID, err)
		}

		prs, err := store.PullRequestsByIssue(ctx, state.Execution.ID, issue.ID)
		if err != nil {
			return nil, fmt.Errorf("engine: load pull requests for issue %s: %w", issue.ID, err)
		}
		if n := len(prs); n > 0 {
			status.PullRequestURL = prs[n-1].URL
		}
		gates, err := store.GateRunsByIssue(ctx, state.Execution.ID, issue.ID)
		if err != nil {
			return nil, fmt.Errorf("engine: load gate runs for issue %s: %w", issue.ID, err)
		}
		reviews, err := store.ReviewRunsByIssueWithoutDiff(ctx, state.Execution.ID, issue.ID)
		if err != nil {
			return nil, fmt.Errorf("engine: load review runs for issue %s: %w", issue.ID, err)
		}
		ciRuns, err := store.CIRunsByIssue(ctx, state.Execution.ID, issue.ID)
		if err != nil {
			return nil, fmt.Errorf("engine: load ci runs for issue %s: %w", issue.ID, err)
		}
		status.Failure = latestFailure(issue.ID, events, gates, reviews, ciRuns)
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func latestFailure(issueID string, events []storage.Event, gates []storage.GateRun, reviews []storage.ReviewRun, ciRuns []storage.CIRun) string {
	for i := len(ciRuns) - 1; i >= 0; i-- {
		if ciRuns[i].Status != storage.CIRunStatusFailed {
			continue
		}
		label := "ci " + ciRuns[i].CheckName
		switch ciRuns[i].Kind {
		case storage.CIRunKindReview:
			label = "review by " + ciRuns[i].CheckName
		case storage.CIRunKindConflict:
			return "pull request has a merge conflict with its base branch"
		}
		if ciRuns[i].Details != "" {
			return fmt.Sprintf("%s failed: %s", label, ciRuns[i].Details)
		}
		return fmt.Sprintf("%s failed", label)
	}
	for i := len(reviews) - 1; i >= 0; i-- {
		// CHANGES_REQUIRED and INCONCLUSIVE are the two review verdicts that
		// can leave an Issue FAILED: the former when the review retry budget is
		// exhausted, the latter (issue #257) when an axis was unrecoverable.
		// Both carry their reason in Summary/Findings.
		if reviews[i].Verdict != "CHANGES_REQUIRED" && reviews[i].Verdict != "INCONCLUSIVE" {
			continue
		}
		if reviews[i].Verdict == "INCONCLUSIVE" {
			if reviews[i].Summary != "" {
				return "review inconclusive: " + reviews[i].Summary
			}
			return "review inconclusive: an axis was unrecoverable (see review run)"
		}
		if reviews[i].Summary != "" {
			return reviews[i].Summary
		}
		if len(reviews[i].Findings) > 0 {
			return reviews[i].Findings[0].Message
		}
	}
	for i := len(gates) - 1; i >= 0; i-- {
		if !gates[i].Passed {
			return fmt.Sprintf("gate %s failed (exit %d)", gates[i].Name, gates[i].ExitCode)
		}
	}
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if event.IssueID != issueID {
			continue
		}
		switch event.Type {
		case "gate.failed":
			var payload struct {
				Name     string `json:"name"`
				ExitCode string `json:"exit_code"`
			}
			if json.Unmarshal([]byte(event.Data), &payload) == nil && payload.Name != "" {
				if payload.ExitCode != "" {
					return fmt.Sprintf("gate %s failed (exit %s)", payload.Name, payload.ExitCode)
				}
				return fmt.Sprintf("gate %s failed", payload.Name)
			}
		case "agent.result":
			var payload struct {
				Status  string `json:"status"`
				Summary string `json:"summary"`
			}
			if json.Unmarshal([]byte(event.Data), &payload) == nil && payload.Status == string(agent.StatusFailed) {
				return payload.Summary
			}
		}
	}
	return ""
}

func buildTelemetry(ctx context.Context, store StatusStore, state storage.ExecutionState, events []storage.Event) (TelemetryReport, error) {
	agentRuns, err := store.AgentRunsByExecution(ctx, state.Execution.ID)
	if err != nil {
		return TelemetryReport{}, fmt.Errorf("engine: load agent runs for execution %s: %w", state.Execution.ID, err)
	}

	gateRunsByIssue := make(map[string][]storage.GateRun, len(state.Issues))
	reviewRunsByIssue := make(map[string][]storage.ReviewRun, len(state.Issues))
	ciRunsByIssue := make(map[string][]storage.CIRun, len(state.Issues))
	for _, issue := range state.Issues {
		gateRuns, err := store.GateRunsByIssue(ctx, state.Execution.ID, issue.ID)
		if err != nil {
			return TelemetryReport{}, fmt.Errorf("engine: load gate runs for issue %s: %w", issue.ID, err)
		}
		reviewRuns, err := store.ReviewRunsByIssueWithoutDiff(ctx, state.Execution.ID, issue.ID)
		if err != nil {
			return TelemetryReport{}, fmt.Errorf("engine: load review runs for issue %s: %w", issue.ID, err)
		}
		ciRuns, err := store.CIRunsByIssue(ctx, state.Execution.ID, issue.ID)
		if err != nil {
			return TelemetryReport{}, fmt.Errorf("engine: load ci runs for issue %s: %w", issue.ID, err)
		}
		gateRunsByIssue[issue.ID] = gateRuns
		reviewRunsByIssue[issue.ID] = reviewRuns
		ciRunsByIssue[issue.ID] = ciRuns
	}

	issueMetrics := make([]IssueTelemetry, 0, len(state.Issues))
	summary := TelemetrySummary{}
	for _, issue := range state.Issues {
		metric := IssueTelemetry{
			IssueID:       issue.ID,
			GateRetries:   issue.RetryBudget.GateFailures(),
			ReviewRetries: issue.RetryBudget.ReviewFailures(),
			CIRetries:     issue.RetryBudget.CIFailures(),
			CycleTime:     cycleTime(events, issue.ID),
		}
		if issue.State == domain.StateDone {
			summary.IssuesCompleted++
		}
		for _, run := range agentRuns {
			if run.IssueID != issue.ID {
				continue
			}
			metric.AgentInvocations++
			metric.ContextBytes += run.ContextBytes
			if run.InputTokens != nil {
				metric.InputTokens += *run.InputTokens
			}
			if run.OutputTokens != nil {
				metric.OutputTokens += *run.OutputTokens
			}
		}
		summary.AgentInvocations += metric.AgentInvocations
		summary.InputTokens += metric.InputTokens
		summary.OutputTokens += metric.OutputTokens
		summary.GateRetries += metric.GateRetries
		summary.ReviewRetries += metric.ReviewRetries
		summary.CIRetries += metric.CIRetries
		summary.ContextBytes += metric.ContextBytes
		issueMetrics = append(issueMetrics, metric)
	}
	if len(events) > 0 {
		summary.WallClockDuration = events[len(events)-1].OccurredAt.Sub(state.Execution.StartedAt)
	}

	structuredEvents := buildStructuredEvents(events, agentRuns, gateRunsByIssue, reviewRunsByIssue, ciRunsByIssue)
	return TelemetryReport{Summary: summary, Issues: issueMetrics, Events: structuredEvents}, nil
}

func cycleTime(events []storage.Event, issueID string) time.Duration {
	var readyAt time.Time
	for _, event := range events {
		if event.IssueID != issueID || event.Type != "issue.transitioned" {
			continue
		}
		var payload struct {
			To string `json:"to"`
		}
		if err := json.Unmarshal([]byte(event.Data), &payload); err != nil {
			continue
		}
		switch domain.IssueState(payload.To) {
		case domain.StateReady:
			if readyAt.IsZero() {
				readyAt = event.OccurredAt
			}
		case domain.StateDone:
			if !readyAt.IsZero() {
				return event.OccurredAt.Sub(readyAt)
			}
		}
	}
	return 0
}

func buildStructuredEvents(events []storage.Event, agentRuns []storage.AgentRun, gateRunsByIssue map[string][]storage.GateRun, reviewRunsByIssue map[string][]storage.ReviewRun, ciRunsByIssue map[string][]storage.CIRun) []StructuredEvent {
	workerByIssue := map[string]string{}
	backendByIssue := map[string]string{}
	stateByIssue := map[string]domain.IssueState{}
	for _, run := range agentRuns {
		if run.Backend != "" && backendByIssue[run.IssueID] == "" {
			backendByIssue[run.IssueID] = run.Backend
		}
	}

	agentIdx := map[string]int{}
	gateIdx := map[string]int{}
	reviewIdx := map[string]int{}
	ciIdx := map[string]int{}

	out := make([]StructuredEvent, 0, len(events))
	for _, event := range events {
		se := StructuredEvent{
			OccurredAt:   event.OccurredAt,
			ExecutionID:  event.ExecutionID,
			IssueID:      event.IssueID,
			Event:        event.Type,
			WorkerID:     workerByIssue[event.IssueID],
			State:        stateByIssue[event.IssueID],
			AgentBackend: backendByIssue[event.IssueID],
		}
		if se.WorkerID == "" && event.IssueID != "" {
			se.WorkerID = workerID(event.ExecutionID, event.IssueID)
		}

		switch event.Type {
		case "issue.claimed":
			var payload struct {
				WorkerRef string `json:"worker_ref"`
			}
			if json.Unmarshal([]byte(event.Data), &payload) == nil && payload.WorkerRef != "" {
				workerByIssue[event.IssueID] = payload.WorkerRef
				se.WorkerID = payload.WorkerRef
			}
		case "issue.transitioned":
			var payload struct {
				To string `json:"to"`
			}
			if json.Unmarshal([]byte(event.Data), &payload) == nil {
				se.State = domain.IssueState(payload.To)
				stateByIssue[event.IssueID] = se.State
			}
		case "agent.run":
			idx := agentIdx[event.IssueID]
			if idx < len(agentRuns) {
				var matched *storage.AgentRun
				for i := idx; i < len(agentRuns); i++ {
					if agentRuns[i].IssueID == event.IssueID {
						matched = &agentRuns[i]
						agentIdx[event.IssueID] = i + 1
						break
					}
				}
				if matched != nil {
					se.Duration = matched.FinishedAt.Sub(matched.StartedAt)
					se.AgentBackend = matched.Backend
				}
			}
		case "gate.run":
			idx := gateIdx[event.IssueID]
			if idx < len(gateRunsByIssue[event.IssueID]) {
				run := gateRunsByIssue[event.IssueID][idx]
				gateIdx[event.IssueID] = idx + 1
				se.Duration = run.FinishedAt.Sub(run.StartedAt)
			}
		case "review.run":
			idx := reviewIdx[event.IssueID]
			if idx < len(reviewRunsByIssue[event.IssueID]) {
				run := reviewRunsByIssue[event.IssueID][idx]
				reviewIdx[event.IssueID] = idx + 1
				se.Duration = run.FinishedAt.Sub(run.StartedAt)
			}
		case "ci.run":
			idx := ciIdx[event.IssueID]
			if idx < len(ciRunsByIssue[event.IssueID]) {
				ciIdx[event.IssueID] = idx + 1
			}
		}

		out = append(out, se)
	}
	return out
}

func workerID(executionID, issueID string) string {
	if executionID == "" || issueID == "" {
		return ""
	}
	return "worker-" + executionID + "-" + issueID
}
