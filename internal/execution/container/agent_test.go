package container

import (
	"context"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/execution"
)

// echoCLIAgent is a minimal agent.Agent double standing in for a real CLI
// Agent Adapter (e.g. internal/agent/claude.Adapter): it calls its
// AgentRunnerFunc exactly the way a CLI Adapter does — passing the
// Issue's Title as stdin (standing in for a prompt) and a fixed arg list
// — then reports the runner's stdout as its Summary. Standing in this way
// proves NewAgentRunner's wiring end-to-end without needing a real CLI
// binary.
type echoCLIAgent struct {
	runner AgentRunnerFunc
}

func (a echoCLIAgent) Execute(ctx context.Context, req agent.AgentRequest) (agent.AgentResult, error) {
	stdout, stderr, exitCode, err := a.runner(ctx, req.WorkspacePath, []string{"-c", "cat"}, req.Issue.Title, []string{"AGENT_VAR=from-agent-request"}, nil)
	if err != nil {
		return agent.AgentResult{}, err
	}
	if exitCode != 0 {
		return agent.AgentResult{Status: agent.StatusFailed, Summary: stderr}, nil
	}
	return agent.AgentResult{Status: agent.StatusImplemented, Summary: stdout}, nil
}

func TestEnvironment_AgentRunsInsideContainerThroughExecute(t *testing.T) {
	runtime := NewFakeRuntime()
	backend, _, _, base := newTestBackendWithRuntime(t, runtime, func(env execution.ExecutionEnvironment) agent.Agent {
		return echoCLIAgent{runner: NewAgentRunner(env, "sh")}
	})
	env, err := backend.Prepare(context.Background(), execution.WorkspaceRequest{
		ExecutionID: "exec1", IssueID: "issue-42", Base: base,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	result, err := env.Agent().Execute(context.Background(), agent.AgentRequest{
		Issue: domain.Issue{Title: "hello from the agent"},
	})
	if err != nil {
		t.Fatalf("Agent().Execute: %v", err)
	}
	if result.Status != agent.StatusImplemented {
		t.Fatalf("Status = %q, want %q (summary: %s)", result.Status, agent.StatusImplemented, result.Summary)
	}
	if result.Summary != "hello from the agent" {
		t.Errorf("Summary = %q, want the echoed prompt", result.Summary)
	}

	executed := runtime.Executed()
	if len(executed) != 1 {
		t.Fatalf("len(Executed()) = %d, want 1 (the Agent ran a command inside the container)", len(executed))
	}
	call := executed[0]
	wantArgs := []string{"sh", "-c", "cat"}
	if len(call.Command.Args) != len(wantArgs) {
		t.Fatalf("Command.Args = %v, want %v", call.Command.Args, wantArgs)
	}
	for i, arg := range wantArgs {
		if call.Command.Args[i] != arg {
			t.Errorf("Command.Args[%d] = %q, want %q", i, call.Command.Args[i], arg)
		}
	}
	if call.Command.Stdin != "hello from the agent" {
		t.Errorf("Command.Stdin = %q, want the Issue title (the prompt)", call.Command.Stdin)
	}
	found := false
	for _, e := range call.Command.Env {
		if e == "AGENT_VAR=from-agent-request" {
			found = true
		}
	}
	if !found {
		t.Errorf("Command.Env = %v, want it to include AGENT_VAR=from-agent-request", call.Command.Env)
	}
}
