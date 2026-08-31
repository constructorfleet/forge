package agent_test

import (
	"context"
	"errors"
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

func TestFakeAgent_ProgramErrorReturnsExactErrorWithoutFallingThroughToDefault(t *testing.T) {
	fake := agent.NewFakeAgent()
	sentinel := errors.New("boom")
	fake.ProgramError("issue-err", sentinel)
	fake.ProgramDefault(agent.AgentResult{Status: agent.StatusImplemented, Summary: "should not be used"})

	result, err := fake.Execute(context.Background(), agent.AgentRequest{Issue: domain.Issue{ID: "issue-err"}})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Execute() error = %v, want %v", err, sentinel)
	}
	if result.Status != "" || result.Summary != "" {
		t.Errorf("Execute() result = %+v, want zero value alongside a programmed error", result)
	}
}

func TestFakeAgent_ProgramDefaultAppliesOnlyToUnprogrammedScenarios(t *testing.T) {
	fake := agent.NewFakeAgent()
	fake.ProgramDefault(agent.AgentResult{Status: agent.StatusFailed, Summary: "default failure"})
	fake.ProgramResult("issue-specific", agent.AgentResult{Status: agent.StatusImplemented, Summary: "specific success"})

	defaultResult, err := fake.Execute(context.Background(), agent.AgentRequest{Issue: domain.Issue{ID: "issue-unprogrammed"}})
	if err != nil {
		t.Fatalf("Execute(unprogrammed) returned unexpected error: %v", err)
	}
	if defaultResult.Status != agent.StatusFailed {
		t.Errorf("unprogrammed scenario Status = %q, want default %q", defaultResult.Status, agent.StatusFailed)
	}

	specificResult, err := fake.Execute(context.Background(), agent.AgentRequest{Issue: domain.Issue{ID: "issue-specific"}})
	if err != nil {
		t.Fatalf("Execute(issue-specific) returned unexpected error: %v", err)
	}
	if specificResult.Status != agent.StatusImplemented {
		t.Errorf("issue-specific Status = %q, want its programmed %q, not the default (precedence)", specificResult.Status, agent.StatusImplemented)
	}
}

func TestFakeAgent_InvocationsReturnsIndependentCopyAcrossCalls(t *testing.T) {
	fake := agent.NewFakeAgent()
	fake.ProgramResult("issue-copy", agent.AgentResult{Status: agent.StatusImplemented})
	req := agent.AgentRequest{WorkspacePath: "/tmp/original", Issue: domain.Issue{ID: "issue-copy"}}
	if _, err := fake.Execute(context.Background(), req); err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}

	got := fake.Invocations()
	got[0].WorkspacePath = "/tmp/mutated"

	again := fake.Invocations()
	if again[0].WorkspacePath != "/tmp/original" {
		t.Errorf("Invocations() = %q after mutating a prior copy's slice element, want %q unaffected", again[0].WorkspacePath, "/tmp/original")
	}
}

func TestFakeAgent_RecordedInvocationIsImmuneToLaterCallerMutation(t *testing.T) {
	fake := agent.NewFakeAgent()
	fake.ProgramResult("issue-mutate", agent.AgentResult{Status: agent.StatusImplemented})

	feedback := []agent.Feedback{{Source: agent.FeedbackSourceGate, Message: "original gate feedback"}}
	gates := []string{"go test ./..."}
	deps := []domain.Dependency{{IssueID: "issue-mutate", DependsOnID: "issue-dep"}}

	req := agent.AgentRequest{
		Issue:      domain.Issue{ID: "issue-mutate", Dependencies: deps},
		Repository: agent.RepositoryContext{QualityGates: gates},
		Feedback:   feedback,
	}

	if _, err := fake.Execute(context.Background(), req); err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}

	// Mutate the caller's backing arrays after the call, as a repair
	// iteration reusing/growing these slices in place might.
	feedback[0].Message = "mutated gate feedback"
	gates[0] = "mutated gate command"
	deps[0].DependsOnID = "mutated-dep"

	got := fake.Invocations()[0]
	if got.Feedback[0].Message != "original gate feedback" {
		t.Errorf("recorded Feedback[0].Message = %q, want %q (immune to later mutation)", got.Feedback[0].Message, "original gate feedback")
	}
	if got.Repository.QualityGates[0] != "go test ./..." {
		t.Errorf("recorded QualityGates[0] = %q, want %q (immune to later mutation)", got.Repository.QualityGates[0], "go test ./...")
	}
	if got.Issue.Dependencies[0].DependsOnID != "issue-dep" {
		t.Errorf("recorded Dependencies[0].DependsOnID = %q, want %q (immune to later mutation)", got.Issue.Dependencies[0].DependsOnID, "issue-dep")
	}
}

func TestFakeAgent_RecordedSemanticDescriptorIsImmuneToLaterSliceGrowth(t *testing.T) {
	fake := agent.NewFakeAgent()
	fake.ProgramResult("issue-semantic", agent.AgentResult{Status: agent.StatusImplemented})

	mcpServers := []agent.MCPServer{{Language: "go", Command: []string{"mcp-lsp"}}}
	nativeServers := []agent.NativeServer{{Language: "go", Command: []string{"gopls"}}}

	req := agent.AgentRequest{
		Issue: domain.Issue{ID: "issue-semantic"},
		Semantic: agent.SemanticDescriptor{
			MCPServers:    mcpServers,
			NativeServers: nativeServers,
		},
	}

	if _, err := fake.Execute(context.Background(), req); err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}

	// Appending to the caller's original slices after the call, as a repair
	// iteration reusing/growing them might, must not retroactively resize
	// the recorded invocation's snapshot.
	mcpServers = append(mcpServers, agent.MCPServer{Language: "python", Command: []string{"mcp-py"}})
	nativeServers = append(nativeServers, agent.NativeServer{Language: "python", Command: []string{"pyright"}})
	if len(mcpServers) != 2 || len(nativeServers) != 2 {
		t.Fatalf("precondition: caller slices should have grown to len 2, got mcp=%d native=%d", len(mcpServers), len(nativeServers))
	}

	got := fake.Invocations()[0]
	if len(got.Semantic.MCPServers) != 1 {
		t.Errorf("recorded Semantic.MCPServers = %+v, want len 1 (immune to later append)", got.Semantic.MCPServers)
	}
	if len(got.Semantic.NativeServers) != 1 {
		t.Errorf("recorded Semantic.NativeServers = %+v, want len 1 (immune to later append)", got.Semantic.NativeServers)
	}
}
