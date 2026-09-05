package tui_test

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
	"github.com/Teagan42/forge/internal/tui"
)

// fakePlanningApprover is a scripted tui.PlanningApprover double.
type fakePlanningApprover struct {
	calls []string
	err   error
}

func (f *fakePlanningApprover) ApprovePlanningArtifact(_ context.Context, featureID string) error {
	f.calls = append(f.calls, featureID)
	return f.err
}

// planningNextPollTick drives one whole poll cycle through the real Init
// command, mirroring nextPollTick.
func planningNextPollTick(t *testing.T, m *tui.PlanningModel) tea.Cmd {
	t.Helper()
	cmd := m.Init()
	if cmd == nil {
		t.Fatalf("Init returned nil cmd")
	}
	_, nextCmd := m.Update(cmd())
	return planningDrainBatch(t, m, nextCmd)
}

func planningDrainBatch(t *testing.T, m *tui.PlanningModel, cmd tea.Cmd) tea.Cmd {
	t.Helper()
	if cmd == nil {
		return nil
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		return cmd
	}
	for _, c := range batch[:len(batch)-1] {
		planningRunImmediateCmd(t, m, c)
	}
	return batch[len(batch)-1]
}

func planningRunImmediateCmd(t *testing.T, m *tui.PlanningModel, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			planningRunImmediateCmd(t, m, c)
		}
		return
	}
	_, next := m.Update(msg)
	planningRunImmediateCmd(t, m, next)
}

// planningFixture builds a PlanningModel over one Feature with two recorded
// planning stages.
func planningFixture(t *testing.T, status domain.PlanningStatus) (*tui.PlanningModel, *fakePlanningRosterStore) {
	t.Helper()
	store := &fakePlanningRosterStore{
		runs: []storage.AgentRun{
			{ID: 1, ExecutionID: "feat-1", IssueID: "feat-1", Backend: "planning", FinishedAt: time.Now()},
		},
		events: map[int64][]storage.TranscriptEvent{
			1: {{Subagent: "decision-resolution"}},
		},
		executions: []domain.PlanningExecution{
			{ID: "planexec-1", FeatureID: "feat-1", Status: status},
		},
	}
	roster := tui.NewPlanningRoster(store)
	return tui.NewPlanningModel(roster, "feat-1", time.Millisecond), store
}

// TestPlanningModelPollTickFetchesAndRenders proves a poll tick resolves
// stage history into the frame and schedules the next tick.
func TestPlanningModelPollTickFetchesAndRenders(t *testing.T) {
	m, _ := planningFixture(t, domain.PlanningStatusActive)
	planningNextPollTick(t, m)
	out := m.View().Content
	if !strings.Contains(out, "decision-resolution") {
		t.Fatalf("expected the recorded stage in the frame, got %q", out)
	}
}

// TestPlanningModelNeverAcceptsCancelKey proves the planning model has no
// cancel control: pressing 'c' does nothing, whatever the Feature's status.
func TestPlanningModelNeverAcceptsCancelKey(t *testing.T) {
	m, _ := planningFixture(t, domain.PlanningStatusActive)
	planningNextPollTick(t, m)
	before := m.View().Content
	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Text: "c", Code: 'c'}))
	if cmd != nil {
		t.Fatalf("the cancel key must produce no command, got one")
	}
	if m.View().Content != before {
		t.Fatalf("the cancel key must never change the frame")
	}
}

// TestPlanningModelApproveKeyCallsApproverWhenLegal proves the approve key
// fires PlanningApprover.ApprovePlanningArtifact only while the Feature's
// latest Planning Execution is parked on NEEDS_APPROVAL.
func TestPlanningModelApproveKeyCallsApproverWhenLegal(t *testing.T) {
	m, _ := planningFixture(t, domain.PlanningStatusNeedsApproval)
	planningNextPollTick(t, m)
	approver := &fakePlanningApprover{}
	m.Approver = approver

	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Text: "p", Code: 'p'}))
	planningRunImmediateCmd(t, m, cmd)

	if len(approver.calls) != 1 || approver.calls[0] != "feat-1" {
		t.Fatalf("ApprovePlanningArtifact calls = %v, want one call for feat-1", approver.calls)
	}
	if !strings.Contains(m.View().Content, "approve requested for feat-1") {
		t.Fatalf("expected an approve-requested notice, got %q", m.View().Content)
	}
}

// TestPlanningModelApproveKeyIgnoredWhenNotLegal proves the approve key does
// nothing while the Feature is not parked on NEEDS_APPROVAL — the same
// state-illegal-key guarantee LegalKeys/IsApproveLegal enforce for Workers.
func TestPlanningModelApproveKeyIgnoredWhenNotLegal(t *testing.T) {
	m, _ := planningFixture(t, domain.PlanningStatusActive)
	planningNextPollTick(t, m)
	approver := &fakePlanningApprover{}
	m.Approver = approver

	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Text: "p", Code: 'p'}))
	planningRunImmediateCmd(t, m, cmd)

	if len(approver.calls) != 0 {
		t.Fatalf("ApprovePlanningArtifact must not fire while not legal, calls = %v", approver.calls)
	}
}

// fakePlanningAnswerer is a scripted tui.Answerer double.
type fakePlanningAnswerer struct {
	calls []string
	err   error
}

func (f *fakePlanningAnswerer) AddComment(_ context.Context, issueID, body string) (tracker.Comment, error) {
	f.calls = append(f.calls, issueID+": "+body)
	if f.err != nil {
		return tracker.Comment{}, f.err
	}
	return tracker.Comment{Author: "forge-bot", Body: body, CreatedAt: time.Now()}, nil
}

// TestPlanningModelAnswerKeyDefersToEditorAndPostsToFeature proves the
// answer key reads the Feature's pending Decision checkpoint, defers it to
// $EDITOR, and — once the editor "closes" — posts the answer as a plain
// comment on the Feature's own tracker issue (mirroring PauseHandler, which
// posts NEEDS_HUMAN comments to FeatureID, never the Decision or Planning
// Execution id).
func TestPlanningModelAnswerKeyDefersToEditorAndPostsToFeature(t *testing.T) {
	m, store := planningFixture(t, domain.PlanningStatusNeedsHuman)
	store.checkpoints = map[string][]storage.DecisionCheckpoint{
		"planexec-1": {
			{ExecutionID: "planexec-1", DecisionID: "decide-1", Question: "Which store engine?"},
		},
	}
	planningNextPollTick(t, m)

	var opened []string
	m.OpenAnswer = func(_, artifact string) tea.Cmd {
		opened = append(opened, artifact)
		return func() tea.Msg { return tui.AnswerClosedMsg{Text: "SQLite, for the embedded footprint."} }
	}
	answerer := &fakePlanningAnswerer{}
	m.Answerer = answerer

	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Text: "a", Code: 'a'}))
	planningRunImmediateCmd(t, m, cmd)

	if len(opened) != 1 || !strings.Contains(opened[0], "Which store engine?") {
		t.Fatalf("editor opened with %v, want the pending decision's question", opened)
	}
	if len(answerer.calls) != 1 || answerer.calls[0] != "feat-1: SQLite, for the embedded footprint." {
		t.Fatalf("AddComment calls = %v, want one plain comment on feat-1", answerer.calls)
	}
}

// TestPlanningModelAnswerKeyIgnoredWhenNoDecisionPending proves the answer
// key opens no editor while the Feature is not parked on NEEDS_HUMAN.
func TestPlanningModelAnswerKeyIgnoredWhenNoDecisionPending(t *testing.T) {
	m, _ := planningFixture(t, domain.PlanningStatusActive)
	planningNextPollTick(t, m)
	opened := 0
	m.OpenAnswer = func(_, _ string) tea.Cmd {
		opened++
		return nil
	}
	m.Answerer = &fakePlanningAnswerer{}

	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Text: "a", Code: 'a'}))
	planningRunImmediateCmd(t, m, cmd)

	if opened != 0 {
		t.Fatalf("editor opened %d times, want 0 while no decision is pending", opened)
	}
}
