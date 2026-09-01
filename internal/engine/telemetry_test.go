package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/ci"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/gate/gatetest"
	"github.com/Teagan42/forge/internal/review"
	"github.com/Teagan42/forge/internal/tracker"
)

type ciTrackerStub struct {
	mergeRequirements tracker.MergeRequirements
	checkResponses    [][]tracker.PullRequestCheck
	mergeCalls        int
	checkCalls        int
}

func (s *ciTrackerStub) GetMergeRequirements(context.Context, string) (tracker.MergeRequirements, error) {
	s.mergeCalls++
	return s.mergeRequirements, nil
}

func (s *ciTrackerStub) GetPullRequestChecks(context.Context, int) ([]tracker.PullRequestCheck, error) {
	if len(s.checkResponses) == 0 {
		return nil, nil
	}
	idx := s.checkCalls
	if idx >= len(s.checkResponses) {
		idx = len(s.checkResponses) - 1
	}
	s.checkCalls++
	return append([]tracker.PullRequestCheck(nil), s.checkResponses[idx]...), nil
}

func steppedClock(times ...time.Time) func() time.Time {
	idx := 0
	return func() time.Time {
		if len(times) == 0 {
			return time.Time{}
		}
		if idx >= len(times) {
			return times[len(times)-1]
		}
		current := times[idx]
		idx++
		return current
	}
}

func TestLoadStatus_TelemetrySummaryAndStructuredEvents(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"33": {ID: "33", Title: "Add telemetry"},
	})
	te.eng.Now = steppedClock(
		time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 28, 12, 0, 1, 0, time.UTC),
		time.Date(2026, 8, 28, 12, 0, 4, 0, time.UTC),
		time.Date(2026, 8, 28, 12, 0, 5, 0, time.UTC),
		time.Date(2026, 8, 28, 12, 0, 6, 0, time.UTC),
		time.Date(2026, 8, 28, 12, 0, 7, 0, time.UTC),
		time.Date(2026, 8, 28, 12, 0, 8, 0, time.UTC),
	)
	te.fake.ProgramResult("33", agent.AgentResult{
		Status:  agent.StatusImplemented,
		Summary: "implemented",
		Usage: &agent.TokenUsage{
			InputTokens:  111,
			OutputTokens: 22,
		},
	})
	te.eng.Config.Quality.Gates = []config.QualityGate{{Name: "test", Command: "make test"}}
	runner := gatetest.NewFakeCommandRunner()
	runner.ProgramResult("make test", 0, "ok", "")
	te.gates.Set(runner)

	reviewer := review.NewFakeReviewer()
	reviewer.ProgramResult("33", review.Result{Verdict: review.VerdictApproved, Summary: "ship it"})
	te.eng.Reviewer = reviewer
	te.eng.Diff = &stubDiff{diff: "diff --git a/main.go b/main.go"}

	pub := &fakePublisher{commitSHA: "abc123"}
	prTracker := newFakePRTracker()
	te.eng.Publisher = pub
	te.eng.PRTracker = prTracker
	te.eng.BaseBranch = "main"

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "33", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	ciTracker := &ciTrackerStub{
		mergeRequirements: tracker.MergeRequirements{RequiredChecks: []string{"build"}},
		checkResponses: [][]tracker.PullRequestCheck{
			{{Name: "build", State: tracker.CheckPending}},
			{{Name: "build", State: tracker.CheckSuccess}},
		},
	}
	supervisor := ci.New(te.store, ciTracker, config.Default(), "main")
	supervisor.Now = steppedClock(
		time.Date(2026, 8, 28, 12, 0, 9, 0, time.UTC),
		time.Date(2026, 8, 28, 12, 0, 10, 0, time.UTC),
	)
	supervisor.Sleep = func(context.Context, time.Duration) error { return nil }

	state, err := supervisor.Wait(ctx, result.ExecutionID, "33")
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if state != domain.StateDone {
		t.Fatalf("state = %s, want DONE", state)
	}

	report, err := engine.LoadStatus(ctx, te.store, result.ExecutionID)
	if err != nil {
		t.Fatalf("LoadStatus: %v", err)
	}
	if report.Telemetry.Summary.IssuesCompleted != 1 {
		t.Fatalf("IssuesCompleted = %d, want 1", report.Telemetry.Summary.IssuesCompleted)
	}
	if report.Telemetry.Summary.AgentInvocations != 1 {
		t.Fatalf("AgentInvocations = %d, want 1", report.Telemetry.Summary.AgentInvocations)
	}
	if report.Telemetry.Summary.InputTokens != 111 || report.Telemetry.Summary.OutputTokens != 22 {
		t.Fatalf("token summary = %+v", report.Telemetry.Summary)
	}
	if report.Telemetry.Summary.ContextBytes <= 0 {
		t.Fatalf("ContextBytes = %d, want > 0", report.Telemetry.Summary.ContextBytes)
	}
	if report.Telemetry.Summary.WallClockDuration <= 0 {
		t.Fatalf("WallClockDuration = %s, want > 0", report.Telemetry.Summary.WallClockDuration)
	}
	if len(report.Telemetry.Issues) != 1 || report.Telemetry.Issues[0].CycleTime <= 0 {
		t.Fatalf("issue telemetry = %+v, want one issue with cycle time", report.Telemetry.Issues)
	}

	var sawAgent, sawGate, sawCI bool
	for _, event := range report.Telemetry.Events {
		if event.ExecutionID != result.ExecutionID {
			t.Fatalf("structured event execution id = %q, want %q", event.ExecutionID, result.ExecutionID)
		}
		if event.IssueID != "33" {
			continue
		}
		switch event.Event {
		case "agent.run":
			sawAgent = true
			if event.AgentBackend != "claude-code" || event.Duration <= 0 || event.WorkerID == "" {
				t.Fatalf("agent structured event = %+v", event)
			}
		case "gate.run":
			sawGate = true
			if event.Duration <= 0 {
				t.Fatalf("gate structured event duration = %s, want > 0", event.Duration)
			}
		case "ci.run":
			sawCI = true
		}
	}
	if !sawAgent || !sawGate || !sawCI {
		t.Fatalf("structured events missing agent/gate/ci: %+v", report.Telemetry.Events)
	}
}
