// Package semantic fulfils Semantic Navigation for an agent invocation (see
// CONTEXT.md "Semantic Navigation", "SemanticProvider"): the seam that
// augments an agent.AgentRequest with a backend-neutral semantic descriptor,
// selected per capability from the configured Agent backend's declared
// Semantic Profile. It sits below internal/engine and imports only
// internal/agent (plus internal/domain transitively through agent.
// RepositoryContext), so it stays a leaf the execution engine wires in
// rather than a dependency any Agent backend needs to know about.
//
// Best-effort throughout: Prepare never errors, and any capability this
// package cannot fill degrades to no semantic navigation for that
// capability rather than failing the Worker.
//
// Wired only into internal/engine's execution path (a Worker's Agent
// call). Planning-agent wiring is explicitly deferred: there is no
// production planningagent.Backend implementing agent.Agent yet for this
// seam to bind to.
package semantic

import (
	"context"

	"github.com/Teagan42/forge/internal/agent"
)

// Provider fulfils Semantic Navigation for one Issue: Prepare is called
// once per Issue (immediately after the Issue's Workspace is created) and
// returns a Session reused for every Agent call across that Issue's repair
// loop.
type Provider interface {
	// Prepare never errors: a backend with no declared SemanticProfile, lsp
	// config that disables navigation, or any other provisioning gap all
	// yield an inert Session rather than a failure.
	Prepare(ctx context.Context, workspacePath string, repo agent.RepositoryContext, servers []DetectedServer) Session
}

// Session augments one Agent call at a time with the Semantic Navigation
// descriptor Prepare computed, and releases whatever it provisioned once
// the Issue's Worker is done with it.
type Session interface {
	// Augment returns req with Semantic set to this Session's descriptor;
	// inert (req returned unchanged) when Prepare found nothing to add.
	Augment(req agent.AgentRequest) agent.AgentRequest

	// Teardown releases anything Prepare provisioned. Called via defer
	// immediately after Prepare — the success path has no symmetric
	// cleanup, so defer is mandatory at the call site.
	Teardown()
}

// DetectedServer is a language server identity — a {Language, Command}
// pair — the Language & server detection seam discovers per Workspace.
// Declared here (ahead of that seam landing) so Provider.Prepare's
// signature is stable; a nil/empty slice degrades Prepare to considering no
// native servers available, never an error.
type DetectedServer struct {
	Language string
	Command  []string
}
