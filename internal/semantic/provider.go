package semantic

import (
	"context"

	"github.com/Teagan42/forge/internal/agent"
)

// ProviderPreference is the operator escape hatch selecting how one
// Semantic Capability is fulfilled, overriding Provider's own selection
// policy for that capability. Mirrors config.LSPProviderPreference; kept as
// its own type so this package's only import stays internal/agent.
type ProviderPreference string

const (
	// ProviderPreferenceForgeManaged forces Provider to fill this
	// capability via the backend's declared InjectionChannel even if the
	// backend already declares native support for it.
	ProviderPreferenceForgeManaged ProviderPreference = "forge-managed"
	// ProviderPreferenceHarnessNative forces Provider to rely purely on
	// whatever the backend natively supports for this capability, even if
	// it is a declared gap — no fill is attempted.
	ProviderPreferenceHarnessNative ProviderPreference = "harness-native"
	// ProviderPreferenceOff disables this capability entirely: never
	// filled, regardless of what the backend declares.
	ProviderPreferenceOff ProviderPreference = "off"
)

// CapabilityOverride force-sets individual Semantic Capabilities for the
// configured backend, overriding its static SemanticProfile declaration. A
// nil pointer means "unmodified". Mirrors config.LSPCapabilityOverride,
// pre-resolved by the caller (cmd/forge) to the single backend Engine runs.
type CapabilityOverride struct {
	Definition      *bool
	References      *bool
	Implementations *bool
	Hover           *bool
	DocumentSymbol  *bool
	WorkspaceSymbol *bool
	CallHierarchy   *bool
	TypeHierarchy   *bool
}

// Config is Provider's resolved configuration, translated by the caller
// (cmd/forge) from the repository's `lsp` config section (config.LSPConfig)
// into this package's backend-neutral shape.
type Config struct {
	// Enabled mirrors config.LSPConfig.Enabled: false makes every Session
	// Prepare returns fully inert, regardless of the backend's declared
	// profile.
	Enabled bool

	// Providers selects, per capability name ("definition", "references",
	// "implementations", "hover", "document_symbol", "workspace_symbol",
	// "call_hierarchy", "type_hierarchy"), which provider fulfills it. A
	// capability absent from this map falls back to Provider's own
	// selection policy: fill a declared gap, leave a native capability
	// alone.
	Providers map[string]ProviderPreference

	// Override force-sets individual Semantic Capabilities for the
	// configured backend, overriding its static declaration.
	Override CapabilityOverride
}

// NewProvider returns the default Provider bound to backend (type-asserted
// for agent.SemanticProfiler inside Prepare) and cfg.
func NewProvider(backend agent.Agent, cfg Config) Provider {
	return &provider{backend: backend, cfg: cfg}
}

type provider struct {
	backend agent.Agent
	cfg     Config
}

func (p *provider) Prepare(_ context.Context, _ string, _ agent.RepositoryContext, servers []DetectedServer) Session {
	if !p.cfg.Enabled {
		return &session{}
	}
	profiler, ok := p.backend.(agent.SemanticProfiler)
	if !ok {
		return &session{}
	}
	return &session{descriptor: buildDescriptor(profiler.SemanticProfile(), p.cfg, servers)}
}

// capabilitySpec pairs one Semantic Capability's name (matching
// config.LSPConfig's vocabulary) with its backend-declared flag and any
// config override for it.
type capabilitySpec struct {
	name     string
	declared bool
	override *bool
}

// buildDescriptor decides, per capability, whether Provider needs to fill a
// gap and via which list (NativeServers for the "lspPlugin" channel,
// MCPServers for "mcp"), then populates that list from every detected
// server — Semantic Capabilities are a per-backend property, not a
// per-language one, so a single fill decision applies uniformly across
// every DetectedServer.
func buildDescriptor(profile agent.SemanticProfile, cfg Config, servers []DetectedServer) agent.SemanticDescriptor {
	caps := profile.Capabilities
	specs := []capabilitySpec{
		{"definition", caps.Definition, cfg.Override.Definition},
		{"references", caps.References, cfg.Override.References},
		{"implementations", caps.Implementations, cfg.Override.Implementations},
		{"hover", caps.Hover, cfg.Override.Hover},
		{"document_symbol", caps.DocumentSymbol, cfg.Override.DocumentSymbol},
		{"workspace_symbol", caps.WorkspaceSymbol, cfg.Override.WorkspaceSymbol},
		{"call_hierarchy", caps.CallHierarchy, cfg.Override.CallHierarchy},
		{"type_hierarchy", caps.TypeHierarchy, cfg.Override.TypeHierarchy},
	}

	var needMCP, needNative bool
	for _, spec := range specs {
		effective := spec.declared
		if spec.override != nil {
			effective = *spec.override
		}
		switch cfg.Providers[spec.name] {
		case ProviderPreferenceOff, ProviderPreferenceHarnessNative:
			continue
		case ProviderPreferenceForgeManaged:
			markFill(profile.Channel, &needMCP, &needNative)
		default:
			if !effective {
				markFill(profile.Channel, &needMCP, &needNative)
			}
		}
	}

	// The native-LSP channel provisions its detected servers independent of
	// any capability gap: Claude drives its own fixed native-LSP operation
	// set regardless of what servers are available, so "lacks an op" and
	// "needs a server" are separate questions on this channel. Provisioning
	// here depends only on the channel and whether anything was detected.
	if profile.Channel == agent.InjectionChannelLSPPlugin && len(servers) > 0 {
		needNative = true
	}

	var descriptor agent.SemanticDescriptor
	if needNative {
		for _, s := range servers {
			descriptor.NativeServers = append(descriptor.NativeServers, agent.NativeServer{Language: s.Language, Command: s.Command})
		}
	}
	if needMCP {
		for _, s := range servers {
			descriptor.MCPServers = append(descriptor.MCPServers, agent.MCPServer{Language: s.Language, Command: s.Command})
		}
	}
	return descriptor
}

// markFill records which descriptor list a gap fill needs, per the
// backend's declared InjectionChannel. InjectionChannelNone (or any other
// unrecognized channel) marks neither: the gap has no way to be filled and
// stays unsupported, which is a safe degradation, never an error.
func markFill(channel agent.InjectionChannel, needMCP, needNative *bool) {
	switch channel {
	case agent.InjectionChannelMCP:
		*needMCP = true
	case agent.InjectionChannelLSPPlugin:
		*needNative = true
	}
}

type session struct {
	descriptor agent.SemanticDescriptor
}

func (s *session) Augment(req agent.AgentRequest) agent.AgentRequest {
	req.Semantic = s.descriptor
	return req
}

func (s *session) Teardown() {}
