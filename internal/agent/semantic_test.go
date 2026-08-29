package agent_test

import (
	"testing"

	"github.com/Teagan42/forge/internal/agent"
)

// stubSemanticProfiler is a minimal SemanticProfiler used only to prove the
// type-assertion seam works: any Agent implementation that also implements
// SemanticProfiler must be discoverable via `agent.(SemanticProfiler)`,
// mirroring tracker.AuthPreflighter's escape-hatch pattern.
type stubSemanticProfiler struct {
	profile agent.SemanticProfile
}

func (s stubSemanticProfiler) SemanticProfile() agent.SemanticProfile {
	return s.profile
}

func TestSemanticProfiler_TypeAssertionSucceedsWhenImplemented(t *testing.T) {
	want := agent.SemanticProfile{
		Capabilities: agent.SemanticCapabilities{
			Definition: true,
			Hover:      true,
		},
		Channel: agent.InjectionChannelMCP,
	}

	var backend interface{} = stubSemanticProfiler{profile: want}

	profiler, ok := backend.(agent.SemanticProfiler)
	if !ok {
		t.Fatalf("type assertion to SemanticProfiler failed for a type that implements it")
	}
	got := profiler.SemanticProfile()
	if got != want {
		t.Fatalf("SemanticProfile() = %+v, want %+v", got, want)
	}
}

func TestSemanticProfiler_TypeAssertionFailsWhenNotImplemented(t *testing.T) {
	var backend interface{} = agent.NewFakeAgent()

	if _, ok := backend.(agent.SemanticProfiler); ok {
		t.Fatalf("FakeAgent unexpectedly implements SemanticProfiler; a backend declaring no profile must be inert")
	}
}

func TestAgentRequest_SemanticZeroValueIsInert(t *testing.T) {
	var req agent.AgentRequest

	if len(req.Semantic.MCPServers) != 0 || len(req.Semantic.NativeServers) != 0 {
		t.Errorf("zero-value AgentRequest.Semantic = %+v, want both lists empty", req.Semantic)
	}
}
