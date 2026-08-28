package agent_test

import (
	"context"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
)

func TestFakeAgent_ReturnsProgrammedImplementedResult(t *testing.T) {
	fake := agent.NewFakeAgent()
	fake.ProgramResult("issue-1", agent.AgentResult{
		Status:  agent.StatusImplemented,
		Summary: "added the widget",
	})

	req := agent.AgentRequest{
		WorkspacePath: "/tmp/workspace-1",
		Issue:         domain.Issue{ID: "issue-1"},
	}

	result, err := fake.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	if result.Status != agent.StatusImplemented {
		t.Errorf("Status = %q, want %q", result.Status, agent.StatusImplemented)
	}
	if result.Summary != "added the widget" {
		t.Errorf("Summary = %q, want %q", result.Summary, "added the widget")
	}
}

func TestFakeAgent_ReturnsProgrammedFailedResult(t *testing.T) {
	fake := agent.NewFakeAgent()
	fake.ProgramResult("issue-2", agent.AgentResult{
		Status:  agent.StatusFailed,
		Summary: "could not apply the patch",
	})

	req := agent.AgentRequest{Issue: domain.Issue{ID: "issue-2"}}

	result, err := fake.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	if result.Status != agent.StatusFailed {
		t.Errorf("Status = %q, want %q", result.Status, agent.StatusFailed)
	}
	if result.Summary != "could not apply the patch" {
		t.Errorf("Summary = %q, want %q", result.Summary, "could not apply the patch")
	}
}

func TestFakeAgent_ReturnsProgrammedNeedsInfoResultWithDetail(t *testing.T) {
	fake := agent.NewFakeAgent()
	fake.ProgramResult("issue-3", agent.AgentResult{
		Status: agent.StatusNeedsInfo,
		NeedsInfo: &agent.NeedsInfoDetail{
			Question: "which auth provider should this use?",
			Context:  "the issue body does not specify a provider",
		},
	})

	req := agent.AgentRequest{Issue: domain.Issue{ID: "issue-3"}}

	result, err := fake.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	if result.Status != agent.StatusNeedsInfo {
		t.Errorf("Status = %q, want %q", result.Status, agent.StatusNeedsInfo)
	}
	if result.NeedsInfo == nil {
		t.Fatal("NeedsInfo = nil, want populated detail")
	}
	if result.NeedsInfo.Question != "which auth provider should this use?" {
		t.Errorf("NeedsInfo.Question = %q, want %q", result.NeedsInfo.Question, "which auth provider should this use?")
	}
}

func TestFakeAgent_RecordsEachInvocationWithItsRequest(t *testing.T) {
	fake := agent.NewFakeAgent()
	fake.ProgramResult("issue-4", agent.AgentResult{Status: agent.StatusImplemented})
	fake.ProgramResult("issue-5", agent.AgentResult{Status: agent.StatusImplemented})

	first := agent.AgentRequest{WorkspacePath: "/tmp/ws-4", Issue: domain.Issue{ID: "issue-4"}}
	second := agent.AgentRequest{WorkspacePath: "/tmp/ws-5", Issue: domain.Issue{ID: "issue-5"}}

	if _, err := fake.Execute(context.Background(), first); err != nil {
		t.Fatalf("Execute(first) returned unexpected error: %v", err)
	}
	if _, err := fake.Execute(context.Background(), second); err != nil {
		t.Fatalf("Execute(second) returned unexpected error: %v", err)
	}

	got := fake.Invocations()
	if len(got) != 2 {
		t.Fatalf("Invocations() len = %d, want 2", len(got))
	}
	if got[0].WorkspacePath != "/tmp/ws-4" || got[0].Issue.ID != "issue-4" {
		t.Errorf("Invocations()[0] = %+v, want workspace /tmp/ws-4 issue issue-4", got[0])
	}
	if got[1].WorkspacePath != "/tmp/ws-5" || got[1].Issue.ID != "issue-5" {
		t.Errorf("Invocations()[1] = %+v, want workspace /tmp/ws-5 issue issue-5", got[1])
	}
}

func TestFakeAgent_ScenariosAreConfiguredIndependently(t *testing.T) {
	fake := agent.NewFakeAgent()
	fake.ProgramResult("issue-implemented", agent.AgentResult{Status: agent.StatusImplemented})
	fake.ProgramResult("issue-failed", agent.AgentResult{Status: agent.StatusFailed})

	implemented, err := fake.Execute(context.Background(), agent.AgentRequest{Issue: domain.Issue{ID: "issue-implemented"}})
	if err != nil {
		t.Fatalf("Execute(issue-implemented) returned unexpected error: %v", err)
	}
	failed, err := fake.Execute(context.Background(), agent.AgentRequest{Issue: domain.Issue{ID: "issue-failed"}})
	if err != nil {
		t.Fatalf("Execute(issue-failed) returned unexpected error: %v", err)
	}

	if implemented.Status != agent.StatusImplemented {
		t.Errorf("issue-implemented Status = %q, want %q", implemented.Status, agent.StatusImplemented)
	}
	if failed.Status != agent.StatusFailed {
		t.Errorf("issue-failed Status = %q, want %q", failed.Status, agent.StatusFailed)
	}
}

func TestFakeAgent_ExecuteErrorsWhenScenarioNotProgrammed(t *testing.T) {
	fake := agent.NewFakeAgent()

	_, err := fake.Execute(context.Background(), agent.AgentRequest{Issue: domain.Issue{ID: "unprogrammed"}})
	if err == nil {
		t.Fatal("Execute() error = nil, want an error for an unprogrammed scenario")
	}
}

func TestFakeAgent_RepeatsFinalQueuedOutcomeOnLaterCalls(t *testing.T) {
	fake := agent.NewFakeAgent()
	fake.ProgramResult("issue-repair", agent.AgentResult{Status: agent.StatusNeedsInfo})
	fake.ProgramResult("issue-repair", agent.AgentResult{Status: agent.StatusImplemented})

	req := agent.AgentRequest{Issue: domain.Issue{ID: "issue-repair"}}

	first, err := fake.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute(first) returned unexpected error: %v", err)
	}
	second, err := fake.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute(second) returned unexpected error: %v", err)
	}
	third, err := fake.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute(third) returned unexpected error: %v", err)
	}

	if first.Status != agent.StatusNeedsInfo {
		t.Errorf("first Status = %q, want %q", first.Status, agent.StatusNeedsInfo)
	}
	if second.Status != agent.StatusImplemented {
		t.Errorf("second Status = %q, want %q", second.Status, agent.StatusImplemented)
	}
	if third.Status != agent.StatusImplemented {
		t.Errorf("third Status = %q, want %q (last queued outcome repeats)", third.Status, agent.StatusImplemented)
	}
}
