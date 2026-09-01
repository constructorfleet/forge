package container

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/execution"
	"github.com/Teagan42/forge/internal/gittest"
)

// TestCredentialBoundary_ContainerNeverSeesSCMOrTrackerCredentials proves,
// by observed behavior rather than code inspection, that a Worker's
// in-container work (Quality Gates and the Agent) never carries an SCM or
// tracker credential into the container, even when one is present in
// Forge's own process environment — the credential boundary
// constructorfleet/forge#335 requires, mirroring LocalHost (ADR 0021).
func TestCredentialBoundary_ContainerNeverSeesSCMOrTrackerCredentials(t *testing.T) {
	const credentialValue = "ghp_super-secret-tracker-token"
	t.Setenv("GITHUB_TOKEN", credentialValue)

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

	// A Quality Gate: engine.runQualityGate never populates Command.Env
	// from Forge's own process environment, so this mirrors production.
	if _, err := env.Execute(context.Background(), execution.Command{Name: "gate", Command: "echo gate ran"}); err != nil {
		t.Fatalf("Execute(gate): %v", err)
	}

	// The Agent: a real CLI Adapter's SanitizedEnv only ever forwards its
	// own declared allowlist (e.g. ANTHROPIC_API_KEY), never
	// GITHUB_TOKEN — mirrored here by the explicit env this call passes.
	if _, err := env.Agent().Execute(context.Background(), agent.AgentRequest{
		Issue: domain.Issue{Title: "do the work"},
	}); err != nil {
		t.Fatalf("Agent().Execute: %v", err)
	}

	for _, call := range runtime.Executed() {
		if strings.Contains(call.Command.Command, credentialValue) || strings.Contains(call.Command.Stdin, credentialValue) {
			t.Errorf("Command %+v carries the tracker credential", call.Command)
		}
		for _, arg := range call.Command.Args {
			if strings.Contains(arg, credentialValue) {
				t.Errorf("Command.Args %v carries the tracker credential", call.Command.Args)
			}
		}
		for _, e := range call.Command.Env {
			if strings.Contains(e, credentialValue) || strings.HasPrefix(e, "GITHUB_TOKEN=") {
				t.Errorf("Command.Env %v carries the tracker credential", call.Command.Env)
			}
		}
	}
}

// TestCredentialBoundary_PublicationNeverTouchesTheContainer proves
// publication (pushing the Worker's commit) happens entirely host-side,
// against the shared git object store, without ever reaching the
// container: the credential-bearing operation and the container's Exec
// seam are, by observed behavior, entirely disjoint.
func TestCredentialBoundary_PublicationNeverTouchesTheContainer(t *testing.T) {
	runtime := NewFakeRuntime()
	backend, _, root, base := newTestBackendWithRuntime(t, runtime, nil)
	env, err := backend.Prepare(context.Background(), execution.WorkspaceRequest{
		ExecutionID: "exec1", IssueID: "issue-42", Base: base,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	// Stage and commit in-container, through Execute, as a Worker's
	// COMMITTING stage would.
	if _, err := env.Execute(context.Background(), execution.Command{Name: "touch", Args: []string{"sh", "-c", "echo hi > file.txt"}}); err != nil {
		t.Fatalf("Execute(touch): %v", err)
	}
	if _, err := env.Execute(context.Background(), execution.Command{Name: "git-add", Command: "git add -A"}); err != nil {
		t.Fatalf("Execute(git add): %v", err)
	}
	if _, err := env.Execute(context.Background(), execution.Command{Name: "git-commit", Command: "git commit -m 'boundary test commit'"}); err != nil {
		t.Fatalf("Execute(git commit): %v", err)
	}
	inContainerCalls := len(runtime.Executed())

	// Publication runs the same way cmd/forge's gitPublisher.Push does for
	// every backend, LocalHost included (ADR 0021): a raw host git
	// command against env.Workspace().Path, never through env.Execute /
	// the container's runtime.
	pushDest := t.TempDir()
	gittest.RunGit(t, pushDest, "init", "-q", "--bare")
	gittest.RunGit(t, root, "remote", "add", "origin", pushDest)
	if out, err := exec.Command("git", "-C", env.Workspace().Path, "push", "origin", "HEAD:refs/heads/published").CombinedOutput(); err != nil {
		t.Fatalf("git push: %v: %s", err, out)
	}

	if got := len(runtime.Executed()); got != inContainerCalls {
		t.Errorf("Executed() grew from %d to %d during Push — publication reached the container", inContainerCalls, got)
	}

	out := gittest.RunGit(t, pushDest, "log", "-1", "--pretty=%s", "refs/heads/published")
	if strings.TrimSpace(out) != "boundary test commit" {
		t.Errorf("pushed remote's branch tip = %q, want the in-container commit", strings.TrimSpace(out))
	}
}
