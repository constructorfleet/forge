package tui

// planning_roster.go implements the planning-phase observation poller: one
// pass reads a Feature's planning AgentRuns and its latest Planning
// Execution status and resolves them into a PlanningViewModel, the same
// store-polling shape roster.go uses for the execution roster (one ~1s
// tick, no filesystem read).

import (
	"context"
	"fmt"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/planningagent"
	"github.com/Teagan42/forge/internal/storage"
)

// PlanningRosterStore is the read-only slice of storage the planning poller
// needs: a Feature's planning AgentRuns, each row's transcript events (the
// only record of its stage), and the Feature's recorded Planning
// Executions, whose latest status gates the approve and answer controls.
type PlanningRosterStore interface {
	AgentRunsByExecution(ctx context.Context, executionID string) ([]storage.AgentRun, error)
	TranscriptEventsByAgentRun(ctx context.Context, executionID, issueID string, agentRunID int64) ([]storage.TranscriptEvent, error)
	ListPlanningExecutionsByFeature(ctx context.Context, featureID string) ([]domain.PlanningExecution, error)

	// GetDecisionCheckpointsByExecution serves the on-request Decision
	// checkpoint read only (see planning_model.go's answer control): the
	// key the answer key defers to $EDITOR.
	GetDecisionCheckpointsByExecution(ctx context.Context, executionID string) ([]storage.DecisionCheckpoint, error)
}

// PlanningRoster fetches a Feature's planning stage history into a
// PlanningViewModel on demand.
type PlanningRoster struct {
	Store PlanningRosterStore

	// Now is the clock PlanningModel measures its transcript pane's commit
	// age against (see PlanningModel's lastCommit), mirroring Roster.Now.
	// Planning claims no heartbeat liveness, so Fetch itself never reads
	// this clock — only the model's own lag stamping does. Defaults to
	// time.Now.
	Now func() time.Time
}

// NewPlanningRoster builds a PlanningRoster over store.
func NewPlanningRoster(store PlanningRosterStore) *PlanningRoster {
	return &PlanningRoster{Store: store, Now: time.Now}
}

// Fetch performs one poll pass: it reloads featureID's planning AgentRuns
// and resolves a PlanningViewModel from them, labelling each row by the
// subagent its transcript recorded and gating the approve/answer controls
// from the Feature's latest Planning Execution status. Position comes from
// this run history, never from planning.DeriveStage (which reads the
// filesystem — the read path is store-only).
func (r *PlanningRoster) Fetch(ctx context.Context, featureID string) (PlanningViewModel, error) {
	runs, err := r.Store.AgentRunsByExecution(ctx, featureID)
	if err != nil {
		return PlanningViewModel{}, fmt.Errorf("tui: agent runs for feature %s: %w", featureID, err)
	}

	var vm PlanningViewModel
	for _, run := range runs {
		if run.Backend != planningagent.TranscriptBackendName {
			continue
		}
		vm.Stages = append(vm.Stages, PlanningStageRow{
			Stage:        r.stageLabel(ctx, featureID, run),
			LastActivity: run.FinishedAt,
		})
	}
	if len(vm.Stages) == 0 {
		vm.Notice = "no planning runs yet"
	}

	vm.ApproveLegal, vm.AnswerLegal, vm.latestExecutionID = r.legality(ctx, featureID)
	return vm, nil
}

// stageLabel reads run's transcript events for the subagent Key that names
// its stage (transcript_events.subagent — the only record of a planning
// run's stage). A read failure or a run with no events yet labels the row
// "unknown" rather than aborting the pass: the roster is an observer.
func (r *PlanningRoster) stageLabel(ctx context.Context, featureID string, run storage.AgentRun) string {
	events, err := r.Store.TranscriptEventsByAgentRun(ctx, featureID, featureID, run.ID)
	if err != nil || len(events) == 0 || events[0].Subagent == "" {
		return "unknown"
	}
	return events[0].Subagent
}

// legality derives the approve/answer control legality, plus the Planning
// Execution id the answer control's DecisionCheckpoint lookup needs, from
// the Feature's latest recorded Planning Execution (the store returns them
// ordered by started_at, id, so the last one is the latest). A Feature with
// no recorded Planning Execution offers neither control.
func (r *PlanningRoster) legality(ctx context.Context, featureID string) (approve, answer bool, executionID string) {
	executions, err := r.Store.ListPlanningExecutionsByFeature(ctx, featureID)
	if err != nil || len(executions) == 0 {
		return false, false, ""
	}
	latest := executions[len(executions)-1]
	return latest.Status == domain.PlanningStatusNeedsApproval, latest.Status == domain.PlanningStatusNeedsHuman, latest.ID
}
