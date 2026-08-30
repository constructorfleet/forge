package agent

// SemanticCapabilities is the flat, one-flag-per-operation record of the
// semantic-navigation operations a backend exposes natively (see
// CONTEXT.md "Semantic Capabilities"). Each field describes what the
// harness itself can already do, independent of any repository or
// language. CallHierarchy covers prepare/incoming/outgoing as a single
// flag; TypeHierarchy is separate.
type SemanticCapabilities struct {
	Definition      bool
	References      bool
	Implementations bool
	Hover           bool
	DocumentSymbol  bool
	WorkspaceSymbol bool
	CallHierarchy   bool
	TypeHierarchy   bool
}

// InjectionChannel identifies the mechanism by which Forge adds semantic
// navigation to a backend that lacks it natively (see CONTEXT.md
// "Injection Channel"). A property of the backend, not of the language or
// repository.
type InjectionChannel string

const (
	// InjectionChannelNone means Forge injects no semantic navigation for
	// this backend.
	InjectionChannelNone InjectionChannel = "none"

	// InjectionChannelMCP means Forge fills capability gaps via a Model
	// Context Protocol server (see CONTEXT.md "Forge-managed Tool
	// Surface").
	InjectionChannelMCP InjectionChannel = "mcp"

	// InjectionChannelLSPPlugin means Forge fills capability gaps via a
	// language-server plugin the harness loads.
	InjectionChannelLSPPlugin InjectionChannel = "lspPlugin"
)

// SemanticProfile is a backend's declared pairing of its Semantic
// Capabilities with its Injection Channel (see CONTEXT.md "Semantic
// Profile") — the single fact the component fulfilling Semantic Navigation
// reads to decide, per capability, whether to rely on the harness or fill
// the gap.
type SemanticProfile struct {
	Capabilities SemanticCapabilities
	Channel      InjectionChannel
}

// SemanticProfiler is an optional capability an Agent Adapter implements
// when it can declare, statically, which semantic-navigation operations it
// exposes natively and how a gap would be injected. The component
// fulfilling Semantic Navigation type-asserts for this interface and reads
// declared capabilities only — never backend identity. An Agent Adapter
// that needs no semantic profile (e.g. a fake/dormant adapter) simply does
// not implement SemanticProfiler — the type assertion fails and the
// backend receives no Semantic Navigation, a safe, inert default, rather
// than a broken one. Mirrors tracker.AuthPreflighter's escape-hatch
// pattern.
type SemanticProfiler interface {
	SemanticProfile() SemanticProfile
}

// NativeServer is one language server identity a SemanticDescriptor asks a
// backend to point its own native tooling at (the "lspPlugin"
// InjectionChannel).
type NativeServer struct {
	Language string
	Command  []string
}

// MCPServer is one MCP endpoint a SemanticDescriptor asks a backend to
// consume semantic navigation through (the "mcp" InjectionChannel).
//
// Forge's own endpoint is a multiplexer covering every language detected in
// the workspace (ADR 0016), so the provider emits exactly one of these
// regardless of language count and leaves both fields empty: which language
// servers run, and how, is resolved by `forge internal-mcp --workspace`
// itself, not named here.
type MCPServer struct {
	Language string
	Command  []string
}

// SemanticDescriptor is the backend-neutral Semantic Navigation payload a
// SemanticProvider seam (internal/semantic) attaches to an AgentRequest via
// Session.Augment. Adapters translate whichever list is populated into
// their own CLI flags/config in later tickets; the provider only decides
// which list to fill. The zero value means "no semantic navigation for
// this request" — the behavior of every Agent call before this field
// existed.
type SemanticDescriptor struct {
	MCPServers    []MCPServer
	NativeServers []NativeServer
}
