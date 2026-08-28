package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

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
	Issues    []domain.Issue
	Telemetry TelemetryReport
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
	AgentRunsByExecution(ctx context.Context, executionID string) ([]storage.AgentRun, error)
	GateRunsByIssue(ctx context.Context, executionID, issueID string) ([]storage.GateRun, error)
	ReviewRunsByIssue(ctx context.Context, executionID, issueID string) ([]storage.ReviewRun, error)
	CIRunsByIssue(ctx context.Context, executionID, issueID string) ([]storage.CIRun, error)
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
	return StatusReport{Execution: state.Execution, Issues: state.Issues, Telemetry: telemetry, Events: events}, nil
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
		reviewRuns, err := store.ReviewRunsByIssue(ctx, state.Execution.ID, issue.ID)
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
