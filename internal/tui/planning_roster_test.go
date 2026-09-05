package tui_test

import (
	"context"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tui"
)

// fakePlanningRosterStore is a scripted PlanningRosterStore double: it holds
// the planning AgentRuns and their transcript events, plus the Feature's
// recorded Planning Executions, so a test can drive the poller's whole
// state-fetch pass deterministically.
type fakePlanningRosterStore struct {
	runs        []storage.AgentRun
	runsErr     error
	events      map[int64][]storage.TranscriptEvent
	executions  []domain.PlanningExecution
	checkpoints map[string][]storage.DecisionCheckpoint
}

func (f *fakePlanningRosterStore) AgentRunsByExecution(context.Context, string) ([]storage.AgentRun, error) {
	if f.runsErr != nil {
		return nil, f.runsErr
	}
	return f.runs, nil
}

func (f *fakePlanningRosterStore) TranscriptEventsByAgentRun(_ context.Context, _, _ string, agentRunID int64) ([]storage.TranscriptEvent, error) {
	return f.events[agentRunID], nil
}

func (f *fakePlanningRosterStore) ListPlanningExecutionsByFeature(context.Context, string) ([]domain.PlanningExecution, error) {
	return f.executions, nil
}

func (f *fakePlanningRosterStore) GetDecisionCheckpointsByExecution(_ context.Context, executionID string) ([]storage.DecisionCheckpoint, error) {
	return f.checkpoints[executionID], nil
}

// TestPlanningRosterFetchBuildsStageHistoryFromRunHistory proves one poller
// pass turns the Feature's planning AgentRuns into a stage-history strip
// labelled by each run's transcript subagent, in run order — position comes
// from this history, not from the filesystem.
func TestPlanningRosterFetchBuildsStageHistoryFromRunHistory(t *testing.T) {
	t1 := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Minute)
	store := &fakePlanningRosterStore{
		runs: []storage.AgentRun{
			{ID: 1, ExecutionID: "feat-1", IssueID: "feat-1", Backend: "planning", FinishedAt: t1},
			{ID: 2, ExecutionID: "feat-1", IssueID: "feat-1", Backend: "planning", FinishedAt: t2},
		},
		events: map[int64][]storage.TranscriptEvent{
			1: {{Subagent: "decision-resolution"}},
			2: {{Subagent: "specification-generation"}},
		},
	}

	roster := tui.NewPlanningRoster(store)
	vm, err := roster.Fetch(context.Background(), "feat-1")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(vm.Stages) != 2 {
		t.Fatalf("Stages = %+v, want 2 rows", vm.Stages)
	}
	if vm.Stages[0].Stage != "decision-resolution" || !vm.Stages[0].LastActivity.Equal(t1) {
		t.Fatalf("Stages[0] = %+v", vm.Stages[0])
	}
	if vm.Stages[1].Stage != "specification-generation" || !vm.Stages[1].LastActivity.Equal(t2) {
		t.Fatalf("Stages[1] = %+v", vm.Stages[1])
	}
}

// TestPlanningRosterFetchIgnoresNonPlanningRuns proves the poller filters
// out any AgentRun whose backend is not "planning" — the coding-agent's own
// runs share the agent_runs table but must never surface in the planning
// strip.
func TestPlanningRosterFetchIgnoresNonPlanningRuns(t *testing.T) {
	store := &fakePlanningRosterStore{
		runs: []storage.AgentRun{
			{ID: 1, Backend: "coding", FinishedAt: time.Now()},
		},
	}
	roster := tui.NewPlanningRoster(store)
	vm, err := roster.Fetch(context.Background(), "feat-1")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(vm.Stages) != 0 {
		t.Fatalf("Stages = %+v, want none", vm.Stages)
	}
	if vm.Notice == "" {
		t.Fatalf("expected a notice for an empty stage history")
	}
}

// TestPlanningRosterFetchGatesControlsFromLatestPlanningExecutionStatus
// proves approve/answer legality is derived from the Feature's latest
// recorded PlanningExecution status alone — never from IssueState, which
// does not exist for planning.
func TestPlanningRosterFetchGatesControlsFromLatestPlanningExecutionStatus(t *testing.T) {
	cases := []struct {
		name        string
		status      domain.PlanningStatus
		wantApprove bool
		wantAnswer  bool
	}{
		{name: "active", status: domain.PlanningStatusActive},
		{name: "needs approval", status: domain.PlanningStatusNeedsApproval, wantApprove: true},
		{name: "needs human", status: domain.PlanningStatusNeedsHuman, wantAnswer: true},
		{name: "complete", status: domain.PlanningStatusComplete},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakePlanningRosterStore{
				executions: []domain.PlanningExecution{
					{ID: "exec-1", FeatureID: "feat-1", Status: tc.status, StartedAt: time.Now()},
				},
			}
			roster := tui.NewPlanningRoster(store)
			vm, err := roster.Fetch(context.Background(), "feat-1")
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if vm.ApproveLegal != tc.wantApprove || vm.AnswerLegal != tc.wantAnswer {
				t.Fatalf("ApproveLegal=%v AnswerLegal=%v, want approve=%v answer=%v",
					vm.ApproveLegal, vm.AnswerLegal, tc.wantApprove, tc.wantAnswer)
			}
		})
	}
}

// TestPlanningRosterFetchPicksLatestPlanningExecutionByOrder proves the
// poller picks the last of several recorded Planning Executions (the store
// returns them ordered by started_at, id), never the first, so a re-plan
// after a completed run is not read as still complete.
func TestPlanningRosterFetchPicksLatestPlanningExecutionByOrder(t *testing.T) {
	store := &fakePlanningRosterStore{
		executions: []domain.PlanningExecution{
			{ID: "exec-1", FeatureID: "feat-1", Status: domain.PlanningStatusComplete},
			{ID: "exec-2", FeatureID: "feat-1", Status: domain.PlanningStatusNeedsApproval},
		},
	}
	roster := tui.NewPlanningRoster(store)
	vm, err := roster.Fetch(context.Background(), "feat-1")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !vm.ApproveLegal {
		t.Fatalf("expected the latest (second) execution's status to gate legality")
	}
}
