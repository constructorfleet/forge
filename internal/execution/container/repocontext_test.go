package container

import (
	"context"
	"testing"

	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/execution"
	"github.com/Teagan42/forge/internal/repocontext"
)

// TestRepositoryContext_CompilesFromTheMountedWorkspace proves the
// Repository Context an in-container Agent sees reflects files the
// container itself wrote into the Workspace, not a stale host-only view —
// the same guarantee LocalHost gives, per the bind mount's shared
// filesystem (constructorfleet/forge#335's "Repository Context is compiled
// from the mounted Workspace" acceptance criterion).
func TestRepositoryContext_CompilesFromTheMountedWorkspace(t *testing.T) {
	backend, _, _, base := newTestBackend(t)
	env, err := backend.Prepare(context.Background(), execution.WorkspaceRequest{
		ExecutionID: "exec1", IssueID: "issue-42", Base: base,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	// Write AGENTS.md "from inside the container", through Execute, rather
	// than through the host filesystem directly.
	instructions := "# Agents\n\nUse tabs, not spaces.\n"
	if _, err := env.Execute(context.Background(), execution.Command{
		Name:  "write-agents-md",
		Args:  []string{"sh", "-c", "cat > AGENTS.md"},
		Stdin: instructions,
	}); err != nil {
		t.Fatalf("Execute(write AGENTS.md): %v", err)
	}

	repo, err := repocontext.Compile(config.Config{}, env.Workspace().Path, base)
	if err != nil {
		t.Fatalf("repocontext.Compile: %v", err)
	}
	const wantInstructions = "# Agents\n\nUse tabs, not spaces."
	if repo.AgentInstructions != wantInstructions {
		t.Errorf("AgentInstructions = %q, want %q (the in-container write)", repo.AgentInstructions, wantInstructions)
	}
}
