// Package mcpserver exposes a toolsurface.Toolset as a Model Context
// Protocol server (github.com/modelcontextprotocol/go-sdk, pinned at
// v1.7.0 per research #89), the backend-neutral tool surface a Codex
// invocation consumes over the "mcp" Injection Channel (see CONTEXT.md and
// issue #127). It registers exactly the tools the underlying Driver
// currently advertises (toolsurface.Toolset.RegisteredTools), so a gopls
// that hasn't (yet) advertised a capability simply omits that tool rather
// than registering one that would fail every call.
package mcpserver

import (
	"context"

	"github.com/Teagan42/forge/internal/semantic/toolsurface"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// serverName/serverVersion identify this MCP server in the initialize
// handshake. Version is fixed at v1 scope (issue #127); it is not derived
// from Forge's own release version.
const (
	serverName    = "forge-lsp"
	serverVersion = "0.1.0"
)

// New returns an MCP server exposing ts's capability-gated tool surface.
func New(ts *toolsurface.Toolset) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: serverName, Version: serverVersion}, nil)

	registered := make(map[string]bool, len(ts.RegisteredTools()))
	for _, name := range ts.RegisteredTools() {
		registered[name] = true
	}

	if registered["find_definition"] {
		addFindDefinition(server, ts)
	}
	if registered["find_references"] {
		addFindReferences(server, ts)
	}
	if registered["find_implementations"] {
		addFindImplementations(server, ts)
	}
	if registered["symbol_info"] {
		addSymbolInfo(server, ts)
	}
	if registered["search_symbols"] {
		addSearchSymbols(server, ts)
	}
	if registered["call_hierarchy"] {
		addCallHierarchy(server, ts)
	}
	if registered["type_hierarchy"] {
		addTypeHierarchy(server, ts)
	}

	return server
}

type locationArgs struct {
	File   string `json:"file" jsonschema:"the file the symbol appears in, relative to the workspace root"`
	Line   int    `json:"line" jsonschema:"the 1-based line the symbol appears on"`
	Symbol string `json:"symbol,omitempty" jsonschema:"the symbol name, to disambiguate multiple symbols on the same line"`
}

func addFindDefinition(server *mcp.Server, ts *toolsurface.Toolset) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "find_definition",
		Description: "Resolve the symbol at a file/line and return its definition as a Source Location with an inlined snippet.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args locationArgs) (*mcp.CallToolResult, toolsurface.SourceLocation, error) {
		loc, err := ts.FindDefinition(ctx, args.File, args.Line, args.Symbol)
		if err != nil {
			return nil, toolsurface.SourceLocation{}, err
		}
		return nil, loc, nil
	})
}

type referencesArgs struct {
	File               string `json:"file" jsonschema:"the file the symbol appears in, relative to the workspace root"`
	Line               int    `json:"line" jsonschema:"the 1-based line the symbol appears on"`
	Symbol             string `json:"symbol,omitempty" jsonschema:"the symbol name, to disambiguate multiple symbols on the same line"`
	IncludeDeclaration bool   `json:"includeDeclaration,omitempty" jsonschema:"whether to include the symbol's own declaration among the results"`
}

func addFindReferences(server *mcp.Server, ts *toolsurface.Toolset) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "find_references",
		Description: "Resolve the symbol at a file/line and return its references as a list of Source Locations.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args referencesArgs) (*mcp.CallToolResult, toolsurface.ListResult, error) {
		result, err := ts.FindReferences(ctx, args.File, args.Line, args.Symbol, args.IncludeDeclaration)
		if err != nil {
			return nil, toolsurface.ListResult{}, err
		}
		return nil, result, nil
	})
}

func addFindImplementations(server *mcp.Server, ts *toolsurface.Toolset) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "find_implementations",
		Description: "Resolve the symbol at a file/line and return its implementations as a list of Source Locations.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args locationArgs) (*mcp.CallToolResult, toolsurface.ListResult, error) {
		result, err := ts.FindImplementations(ctx, args.File, args.Line, args.Symbol)
		if err != nil {
			return nil, toolsurface.ListResult{}, err
		}
		return nil, result, nil
	})
}

type symbolInfoArgs struct {
	File string `json:"file" jsonschema:"the file the symbol appears in, relative to the workspace root"`
	Line int    `json:"line" jsonschema:"the 1-based line the symbol appears on"`
}

func addSymbolInfo(server *mcp.Server, ts *toolsurface.Toolset) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "symbol_info",
		Description: "Resolve the symbol at a file/line and return its signature, documentation, and definition location.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args symbolInfoArgs) (*mcp.CallToolResult, toolsurface.SymbolInfoResult, error) {
		result, err := ts.SymbolInfo(ctx, args.File, args.Line)
		if err != nil {
			return nil, toolsurface.SymbolInfoResult{}, err
		}
		return nil, result, nil
	})
}

type searchSymbolsArgs struct {
	Query string `json:"query,omitempty" jsonschema:"the symbol name (sub)string to search for"`
	Scope string `json:"scope" jsonschema:"either 'file' or 'workspace'"`
	File  string `json:"file,omitempty" jsonschema:"the file to search, required when scope is 'file'"`
}

func addSearchSymbols(server *mcp.Server, ts *toolsurface.Toolset) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_symbols",
		Description: "Search for symbols matching a query, scoped to one file or the whole workspace.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args searchSymbolsArgs) (*mcp.CallToolResult, toolsurface.ListResult, error) {
		result, err := ts.SearchSymbols(ctx, args.Query, toolsurface.Scope(args.Scope), args.File)
		if err != nil {
			return nil, toolsurface.ListResult{}, err
		}
		return nil, result, nil
	})
}

type hierarchyArgs struct {
	File      string `json:"file" jsonschema:"the file the symbol appears in, relative to the workspace root"`
	Line      int    `json:"line" jsonschema:"the 1-based line the symbol appears on"`
	Direction string `json:"direction" jsonschema:"call_hierarchy: 'incoming' or 'outgoing'; type_hierarchy: 'super' or 'sub'"`
}

func addCallHierarchy(server *mcp.Server, ts *toolsurface.Toolset) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "call_hierarchy",
		Description: "Resolve the symbol at a file/line and return its callers ('incoming') or callees ('outgoing').",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args hierarchyArgs) (*mcp.CallToolResult, toolsurface.ListResult, error) {
		result, err := ts.CallHierarchy(ctx, args.File, args.Line, toolsurface.Direction(args.Direction))
		if err != nil {
			return nil, toolsurface.ListResult{}, err
		}
		return nil, result, nil
	})
}

func addTypeHierarchy(server *mcp.Server, ts *toolsurface.Toolset) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "type_hierarchy",
		Description: "Resolve the symbol at a file/line and return its supertypes ('super') or subtypes ('sub').",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args hierarchyArgs) (*mcp.CallToolResult, toolsurface.ListResult, error) {
		result, err := ts.TypeHierarchy(ctx, args.File, args.Line, toolsurface.Direction(args.Direction))
		if err != nil {
			return nil, toolsurface.ListResult{}, err
		}
		return nil, result, nil
	})
}
