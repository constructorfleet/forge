package semantic_test

import (
	"context"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/semantic"
)

// profiledBackend is a minimal agent.Agent double that also declares an
// agent.SemanticProfile, so Provider can discover it via type assertion.
type profiledBackend struct {
	agent.Agent
	profile agent.SemanticProfile
}

func (b profiledBackend) SemanticProfile() agent.SemanticProfile {
	return b.profile
}

// allCapabilitiesTrue returns a SemanticCapabilities with every flag set,
// so a test isolating one capability's behavior isn't muddied by the other
// seven defaulting to false (an unrelated gap).
func allCapabilitiesTrue() agent.SemanticCapabilities {
	return agent.SemanticCapabilities{
		Definition:      true,
		References:      true,
		Implementations: true,
		Hover:           true,
		DocumentSymbol:  true,
		WorkspaceSymbol: true,
		CallHierarchy:   true,
		TypeHierarchy:   true,
	}
}

func TestPrepare_BackendWithNoProfile_ReturnsInertSession(t *testing.T) {
	provider := semantic.NewProvider(agent.NewFakeAgent(), semantic.Config{Enabled: true})

	sess := provider.Prepare(context.Background(), "/workspace", agent.RepositoryContext{}, []semantic.DetectedServer{
		{Language: "go", Command: []string{"gopls"}},
	})
	defer sess.Teardown()

	augmented := sess.Augment(agent.AgentRequest{})
	if len(augmented.Semantic.MCPServers) != 0 || len(augmented.Semantic.NativeServers) != 0 {
		t.Errorf("Augment() = %+v, want an inert descriptor for a backend with no SemanticProfile", augmented.Semantic)
	}
}

func TestPrepare_Disabled_ReturnsInertSessionEvenWithProfile(t *testing.T) {
	backend := profiledBackend{profile: agent.SemanticProfile{
		Capabilities: agent.SemanticCapabilities{Definition: false},
		Channel:      agent.InjectionChannelLSPPlugin,
	}}
	provider := semantic.NewProvider(backend, semantic.Config{Enabled: false})

	sess := provider.Prepare(context.Background(), "/workspace", agent.RepositoryContext{}, []semantic.DetectedServer{
		{Language: "go", Command: []string{"gopls"}},
	})
	defer sess.Teardown()

	augmented := sess.Augment(agent.AgentRequest{})
	if len(augmented.Semantic.MCPServers) != 0 || len(augmented.Semantic.NativeServers) != 0 {
		t.Errorf("Augment() = %+v, want an inert descriptor when lsp.enabled is false", augmented.Semantic)
	}
}

func TestPrepare_GapWithLSPPluginChannel_FillsNativeServers(t *testing.T) {
	backend := profiledBackend{profile: agent.SemanticProfile{
		Capabilities: agent.SemanticCapabilities{Definition: false},
		Channel:      agent.InjectionChannelLSPPlugin,
	}}
	provider := semantic.NewProvider(backend, semantic.Config{Enabled: true})

	sess := provider.Prepare(context.Background(), "/workspace", agent.RepositoryContext{}, []semantic.DetectedServer{
		{Language: "go", Command: []string{"gopls"}},
	})
	defer sess.Teardown()

	augmented := sess.Augment(agent.AgentRequest{})
	if len(augmented.Semantic.NativeServers) != 1 || augmented.Semantic.NativeServers[0].Language != "go" {
		t.Errorf("NativeServers = %+v, want one {go, [gopls]} entry", augmented.Semantic.NativeServers)
	}
	if len(augmented.Semantic.MCPServers) != 0 {
		t.Errorf("MCPServers = %+v, want none (this backend's channel is lspPlugin)", augmented.Semantic.MCPServers)
	}
}

func TestPrepare_GapWithMCPChannel_FillsMCPServers(t *testing.T) {
	backend := profiledBackend{profile: agent.SemanticProfile{
		Capabilities: agent.SemanticCapabilities{}, // all false
		Channel:      agent.InjectionChannelMCP,
	}}
	provider := semantic.NewProvider(backend, semantic.Config{Enabled: true})

	sess := provider.Prepare(context.Background(), "/workspace", agent.RepositoryContext{}, []semantic.DetectedServer{
		{Language: "go", Command: []string{"gopls"}},
	})
	defer sess.Teardown()

	augmented := sess.Augment(agent.AgentRequest{})
	if len(augmented.Semantic.MCPServers) != 1 || augmented.Semantic.MCPServers[0].Language != "go" {
		t.Errorf("MCPServers = %+v, want one {go, [gopls]} entry", augmented.Semantic.MCPServers)
	}
	if len(augmented.Semantic.NativeServers) != 0 {
		t.Errorf("NativeServers = %+v, want none (this backend's channel is mcp)", augmented.Semantic.NativeServers)
	}
}

func TestPrepare_CapabilityAlreadyNative_NeedsNoFill(t *testing.T) {
	backend := profiledBackend{profile: agent.SemanticProfile{
		Capabilities: allCapabilitiesTrue(),
		Channel:      agent.InjectionChannelLSPPlugin,
	}}
	provider := semantic.NewProvider(backend, semantic.Config{Enabled: true})

	sess := provider.Prepare(context.Background(), "/workspace", agent.RepositoryContext{}, []semantic.DetectedServer{
		{Language: "go", Command: []string{"gopls"}},
	})
	defer sess.Teardown()

	augmented := sess.Augment(agent.AgentRequest{})
	if len(augmented.Semantic.NativeServers) != 0 || len(augmented.Semantic.MCPServers) != 0 {
		t.Errorf("Augment() = %+v, want an inert descriptor: the backend already handles Definition itself", augmented.Semantic)
	}
}

func TestPrepare_ChannelNone_GapStaysUnfilled(t *testing.T) {
	backend := profiledBackend{profile: agent.SemanticProfile{
		Capabilities: agent.SemanticCapabilities{Definition: false},
		Channel:      agent.InjectionChannelNone,
	}}
	provider := semantic.NewProvider(backend, semantic.Config{Enabled: true})

	sess := provider.Prepare(context.Background(), "/workspace", agent.RepositoryContext{}, []semantic.DetectedServer{
		{Language: "go", Command: []string{"gopls"}},
	})
	defer sess.Teardown()

	augmented := sess.Augment(agent.AgentRequest{})
	if len(augmented.Semantic.NativeServers) != 0 || len(augmented.Semantic.MCPServers) != 0 {
		t.Errorf("Augment() = %+v, want an inert descriptor: the backend has no injection channel to fill the gap", augmented.Semantic)
	}
}

func TestPrepare_ProviderPreferenceOff_SkipsFillEvenForAGap(t *testing.T) {
	caps := allCapabilitiesTrue()
	caps.Definition = false
	backend := profiledBackend{profile: agent.SemanticProfile{
		Capabilities: caps,
		Channel:      agent.InjectionChannelMCP,
	}}
	provider := semantic.NewProvider(backend, semantic.Config{
		Enabled:   true,
		Providers: map[string]semantic.ProviderPreference{"definition": semantic.ProviderPreferenceOff},
	})

	sess := provider.Prepare(context.Background(), "/workspace", agent.RepositoryContext{}, []semantic.DetectedServer{
		{Language: "go", Command: []string{"gopls"}},
	})
	defer sess.Teardown()

	augmented := sess.Augment(agent.AgentRequest{})
	if len(augmented.Semantic.MCPServers) != 0 {
		t.Errorf("MCPServers = %+v, want none: the operator forced definition off", augmented.Semantic.MCPServers)
	}
}

func TestPrepare_CapabilityOverride_ForcesGapFilling(t *testing.T) {
	backend := profiledBackend{profile: agent.SemanticProfile{
		Capabilities: allCapabilitiesTrue(),
		Channel:      agent.InjectionChannelMCP,
	}}
	forceOff := false
	provider := semantic.NewProvider(backend, semantic.Config{
		Enabled:  true,
		Override: semantic.CapabilityOverride{Definition: &forceOff},
	})

	sess := provider.Prepare(context.Background(), "/workspace", agent.RepositoryContext{}, []semantic.DetectedServer{
		{Language: "go", Command: []string{"gopls"}},
	})
	defer sess.Teardown()

	augmented := sess.Augment(agent.AgentRequest{})
	if len(augmented.Semantic.MCPServers) != 1 {
		t.Errorf("MCPServers = %+v, want one entry: the lsp config override forced Definition to false, reopening the gap", augmented.Semantic.MCPServers)
	}
}

func TestPrepare_ForgeManagedPreference_ForcesFillEvenWhenNative(t *testing.T) {
	backend := profiledBackend{profile: agent.SemanticProfile{
		Capabilities: allCapabilitiesTrue(),
		Channel:      agent.InjectionChannelLSPPlugin,
	}}
	provider := semantic.NewProvider(backend, semantic.Config{
		Enabled:   true,
		Providers: map[string]semantic.ProviderPreference{"definition": semantic.ProviderPreferenceForgeManaged},
	})

	sess := provider.Prepare(context.Background(), "/workspace", agent.RepositoryContext{}, []semantic.DetectedServer{
		{Language: "go", Command: []string{"gopls"}},
	})
	defer sess.Teardown()

	augmented := sess.Augment(agent.AgentRequest{})
	if len(augmented.Semantic.NativeServers) != 1 {
		t.Errorf("NativeServers = %+v, want one entry: forge-managed forces Forge's own server even though the backend already supports Definition natively", augmented.Semantic.NativeServers)
	}
}

func TestPrepare_NoDetectedServers_DegradesToNoFillWithoutError(t *testing.T) {
	backend := profiledBackend{profile: agent.SemanticProfile{
		Capabilities: agent.SemanticCapabilities{Definition: false},
		Channel:      agent.InjectionChannelLSPPlugin,
	}}
	provider := semantic.NewProvider(backend, semantic.Config{Enabled: true})

	// No detected servers at all — the Language & server detection seam
	// found nothing (or hasn't landed yet). Best-effort: still no error,
	// still an inert (but non-nil) Session.
	sess := provider.Prepare(context.Background(), "/workspace", agent.RepositoryContext{}, nil)
	defer sess.Teardown()

	augmented := sess.Augment(agent.AgentRequest{})
	if len(augmented.Semantic.NativeServers) != 0 {
		t.Errorf("NativeServers = %+v, want none: nothing was detected to fill the gap with", augmented.Semantic.NativeServers)
	}
}

func TestAugment_InertSessionLeavesRequestOtherwiseUnchanged(t *testing.T) {
	provider := semantic.NewProvider(agent.NewFakeAgent(), semantic.Config{Enabled: true})
	sess := provider.Prepare(context.Background(), "/workspace", agent.RepositoryContext{}, nil)
	defer sess.Teardown()

	req := agent.AgentRequest{WorkspacePath: "/workspace/issue-1"}
	got := sess.Augment(req)
	if got.WorkspacePath != req.WorkspacePath {
		t.Errorf("Augment() mutated WorkspacePath: got %q, want %q", got.WorkspacePath, req.WorkspacePath)
	}
}
