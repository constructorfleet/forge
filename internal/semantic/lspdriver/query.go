package lspdriver

import (
	"context"
	"errors"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// ErrCapabilityUnsupported is returned by FindImplementations,
// CallHierarchy, and TypeHierarchy when the connected server didn't
// advertise the corresponding capability, so no request is attempted.
var ErrCapabilityUnsupported = errors.New("lspdriver: capability not supported by server")

// Location is one file:line result in this package's typed shape.
type Location struct {
	File     string
	Position Position
}

// Symbol is one entry from DocumentSymbols or WorkspaceSymbols.
type Symbol struct {
	Name     string
	Kind     string
	Location Location
}

// SymbolInfo is the result of SymbolInfo: hover's signature/type and docs,
// combined with the symbol's definition location.
type SymbolInfo struct {
	Signature     string
	Documentation string
	Definition    *Location
}

// HierarchyItem is one node in a call or type hierarchy.
type HierarchyItem struct {
	Name     string
	Detail   string
	Location Location
}

// CallHierarchy is the result of CallHierarchy: the item requested plus its
// direct callers (incoming calls) and callees (outgoing calls).
type CallHierarchy struct {
	Item    HierarchyItem
	Callers []HierarchyItem
	Callees []HierarchyItem
}

// TypeHierarchy is the result of TypeHierarchy: the item requested plus its
// direct supertypes and subtypes.
type TypeHierarchy struct {
	Item       HierarchyItem
	Supertypes []HierarchyItem
	Subtypes   []HierarchyItem
}

// FindDefinition maps to textDocument/definition.
func (d *Driver) FindDefinition(ctx context.Context, file string, pos Position) ([]Location, error) {
	server, docURI, err := d.prepare(ctx, file)
	if err != nil {
		return nil, err
	}

	result, err := server.Definition(ctx, &protocol.DefinitionParams{
		TextDocumentPositionParams: textDocumentPosition(docURI, pos),
	})
	if err != nil {
		return nil, err
	}
	return definitionResultLocations(result), nil
}

// FindReferences maps to textDocument/references.
func (d *Driver) FindReferences(ctx context.Context, file string, pos Position) ([]Location, error) {
	server, docURI, err := d.prepare(ctx, file)
	if err != nil {
		return nil, err
	}

	result, err := server.References(ctx, &protocol.ReferenceParams{
		TextDocumentPositionParams: textDocumentPosition(docURI, pos),
		Context:                    protocol.ReferenceContext{IncludeDeclaration: true},
	})
	if err != nil {
		return nil, err
	}

	locs := make([]Location, 0, len(result))
	for _, l := range result {
		locs = append(locs, fromLSPLocation(l))
	}
	return locs, nil
}

// FindImplementations maps to textDocument/implementation. It returns
// ErrCapabilityUnsupported if the connected server didn't advertise
// implementationProvider (e.g. pyright), matching the CallHierarchy/
// TypeHierarchy capability gates.
func (d *Driver) FindImplementations(ctx context.Context, file string, pos Position) ([]Location, error) {
	if d.Capabilities().ImplementationProvider == nil {
		return nil, ErrCapabilityUnsupported
	}

	server, docURI, err := d.prepare(ctx, file)
	if err != nil {
		return nil, err
	}

	result, err := server.Implementation(ctx, &protocol.ImplementationParams{
		TextDocumentPositionParams: textDocumentPosition(docURI, pos),
	})
	if err != nil {
		return nil, err
	}
	return definitionResultLocations(result), nil
}

// SymbolInfo combines textDocument/hover (signature/type/docs) with
// textDocument/definition (definition location) for the symbol at pos.
func (d *Driver) SymbolInfo(ctx context.Context, file string, pos Position) (SymbolInfo, error) {
	server, docURI, err := d.prepare(ctx, file)
	if err != nil {
		return SymbolInfo{}, err
	}

	var info SymbolInfo

	hover, err := server.Hover(ctx, &protocol.HoverParams{
		TextDocumentPositionParams: textDocumentPosition(docURI, pos),
	})
	if err != nil {
		return SymbolInfo{}, err
	}
	if hover != nil {
		info.Signature, info.Documentation = splitHoverContents(d.opts.Profile.HoverStyle, hover.Contents)
	}

	defResult, err := server.Definition(ctx, &protocol.DefinitionParams{
		TextDocumentPositionParams: textDocumentPosition(docURI, pos),
	})
	if err != nil {
		return SymbolInfo{}, err
	}
	if locs := definitionResultLocations(defResult); len(locs) > 0 {
		info.Definition = &locs[0]
	}

	return info, nil
}

// DocumentSymbols maps to textDocument/documentSymbol, flattening any
// hierarchical result into a single list.
func (d *Driver) DocumentSymbols(ctx context.Context, file string) ([]Symbol, error) {
	server, docURI, err := d.prepare(ctx, file)
	if err != nil {
		return nil, err
	}

	result, err := server.DocumentSymbol(ctx, &protocol.DocumentSymbolParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: docURI},
	})
	if err != nil {
		return nil, err
	}
	return documentSymbolResultSymbols(docURI, result, d.opts.Profile.DropSymbolChildren), nil
}

// WorkspaceSymbols maps to workspace/symbol.
func (d *Driver) WorkspaceSymbols(ctx context.Context, query string) ([]Symbol, error) {
	server, err := d.readyServer()
	if err != nil {
		return nil, err
	}

	result, err := server.Symbols(ctx, &protocol.WorkspaceSymbolParams{Query: query})
	if err != nil {
		return nil, err
	}
	return workspaceSymbolResultSymbols(result), nil
}

// CallHierarchy maps to textDocument/prepareCallHierarchy followed by
// callHierarchy/incomingCalls and callHierarchy/outgoingCalls on the
// prepared item.
func (d *Driver) CallHierarchy(ctx context.Context, file string, pos Position) (CallHierarchy, error) {
	if d.Capabilities().CallHierarchyProvider == nil {
		return CallHierarchy{}, ErrCapabilityUnsupported
	}

	server, docURI, err := d.prepare(ctx, file)
	if err != nil {
		return CallHierarchy{}, err
	}

	items, err := server.PrepareCallHierarchy(ctx, &protocol.CallHierarchyPrepareParams{
		TextDocumentPositionParams: textDocumentPosition(docURI, pos),
	})
	if err != nil {
		return CallHierarchy{}, err
	}
	if len(items) == 0 {
		return CallHierarchy{}, nil
	}
	item := items[0]

	incoming, err := server.IncomingCalls(ctx, &protocol.CallHierarchyIncomingCallsParams{Item: item})
	if err != nil {
		return CallHierarchy{}, err
	}
	outgoing, err := server.OutgoingCalls(ctx, &protocol.CallHierarchyOutgoingCallsParams{Item: item})
	if err != nil {
		return CallHierarchy{}, err
	}

	result := CallHierarchy{Item: callHierarchyItemToHierarchyItem(item)}
	for _, c := range incoming {
		result.Callers = append(result.Callers, callHierarchyItemToHierarchyItem(c.From))
	}
	for _, c := range outgoing {
		result.Callees = append(result.Callees, callHierarchyItemToHierarchyItem(c.To))
	}
	return result, nil
}

// TypeHierarchy maps to textDocument/prepareTypeHierarchy followed by
// typeHierarchy/supertypes and typeHierarchy/subtypes on the prepared item.
func (d *Driver) TypeHierarchy(ctx context.Context, file string, pos Position) (TypeHierarchy, error) {
	if d.Capabilities().TypeHierarchyProvider == nil {
		return TypeHierarchy{}, ErrCapabilityUnsupported
	}

	server, docURI, err := d.prepare(ctx, file)
	if err != nil {
		return TypeHierarchy{}, err
	}

	items, err := server.PrepareTypeHierarchy(ctx, &protocol.TypeHierarchyPrepareParams{
		TextDocumentPositionParams: textDocumentPosition(docURI, pos),
	})
	if err != nil {
		return TypeHierarchy{}, err
	}
	if len(items) == 0 {
		return TypeHierarchy{}, nil
	}
	item := items[0]

	supertypes, err := server.Supertypes(ctx, &protocol.TypeHierarchySupertypesParams{Item: item})
	if err != nil {
		return TypeHierarchy{}, err
	}
	subtypes, err := server.Subtypes(ctx, &protocol.TypeHierarchySubtypesParams{Item: item})
	if err != nil {
		return TypeHierarchy{}, err
	}

	result := TypeHierarchy{Item: typeHierarchyItemToHierarchyItem(item)}
	for _, t := range supertypes {
		result.Supertypes = append(result.Supertypes, typeHierarchyItemToHierarchyItem(t))
	}
	for _, t := range subtypes {
		result.Subtypes = append(result.Subtypes, typeHierarchyItemToHierarchyItem(t))
	}
	return result, nil
}

// prepare resolves the live server and ensures file has been opened
// (lazily, at most once) before a query against it.
func (d *Driver) prepare(ctx context.Context, file string) (protocol.Server, uri.URI, error) {
	server, err := d.readyServer()
	if err != nil {
		return nil, "", err
	}
	docURI, err := d.ensureOpen(ctx, server, file)
	if err != nil {
		return nil, "", err
	}
	return server, docURI, nil
}

func textDocumentPosition(docURI uri.URI, pos Position) protocol.TextDocumentPositionParams {
	return protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: docURI},
		Position:     toLSPPosition(pos),
	}
}

func fromLSPLocation(loc protocol.Location) Location {
	return Location{File: loc.URI.FsPath(), Position: fromLSPPosition(loc.Range.Start)}
}

// definitionResultLocations normalizes the DefinitionResult union
// (*Location, LocationSlice, or DefinitionLinkSlice — textDocument/
// definition and textDocument/implementation share this result shape) into
// this package's typed Location list.
func definitionResultLocations(result protocol.DefinitionResult) []Location {
	switch v := result.(type) {
	case *protocol.Location:
		if v == nil {
			return nil
		}
		return []Location{fromLSPLocation(*v)}
	case protocol.LocationSlice:
		locs := make([]Location, 0, len(v))
		for _, l := range v {
			locs = append(locs, fromLSPLocation(l))
		}
		return locs
	case protocol.DefinitionLinkSlice:
		locs := make([]Location, 0, len(v))
		for _, l := range v {
			locs = append(locs, Location{
				File:     l.TargetURI.FsPath(),
				Position: fromLSPPosition(l.TargetRange.Start),
			})
		}
		return locs
	default:
		return nil
	}
}

// documentSymbolResultSymbols normalizes the DocumentSymbolResult union
// (SymbolInformationSlice or the hierarchical DocumentSymbolSlice) into a
// flat Symbol list, associated with file since DocumentSymbol entries don't
// carry their own URI. dropChildren, from ServerProfile.DropSymbolChildren,
// excludes nested symbols (e.g. pyright's function parameters) when true.
func documentSymbolResultSymbols(file uri.URI, result protocol.DocumentSymbolResult, dropChildren bool) []Symbol {
	switch v := result.(type) {
	case protocol.DocumentSymbolSlice:
		var out []Symbol
		var walk func([]protocol.DocumentSymbol)
		walk = func(syms []protocol.DocumentSymbol) {
			for _, s := range syms {
				out = append(out, Symbol{
					Name: s.Name,
					Kind: symbolKindName(s.Kind),
					Location: Location{
						File:     file.FsPath(),
						Position: fromLSPPosition(s.SelectionRange.Start),
					},
				})
				if len(s.Children) > 0 && !dropChildren {
					walk(s.Children)
				}
			}
		}
		walk(v)
		return out
	case protocol.SymbolInformationSlice:
		return symbolInformationSlice(v)
	default:
		return nil
	}
}

// workspaceSymbolResultSymbols normalizes the WorkspaceSymbolResult union
// (SymbolInformationSlice or WorkspaceSymbolSlice) into a flat Symbol list.
func workspaceSymbolResultSymbols(result protocol.WorkspaceSymbolResult) []Symbol {
	switch v := result.(type) {
	case protocol.SymbolInformationSlice:
		return symbolInformationSlice(v)
	case protocol.WorkspaceSymbolSlice:
		out := make([]Symbol, 0, len(v))
		for _, s := range v {
			out = append(out, Symbol{
				Name:     s.Name,
				Kind:     symbolKindName(s.Kind),
				Location: workspaceSymbolLocation(s.Location),
			})
		}
		return out
	default:
		return nil
	}
}

func symbolInformationSlice(v protocol.SymbolInformationSlice) []Symbol {
	out := make([]Symbol, 0, len(v))
	for _, s := range v {
		out = append(out, Symbol{
			Name:     s.Name,
			Kind:     symbolKindName(s.Kind),
			Location: fromLSPLocation(s.Location),
		})
	}
	return out
}

func workspaceSymbolLocation(loc protocol.WorkspaceSymbolLocation) Location {
	switch v := loc.(type) {
	case *protocol.Location:
		if v == nil {
			return Location{}
		}
		return fromLSPLocation(*v)
	case *protocol.LocationUriOnly:
		if v == nil {
			return Location{}
		}
		return Location{File: v.URI.FsPath()}
	default:
		return Location{}
	}
}

func callHierarchyItemToHierarchyItem(item protocol.CallHierarchyItem) HierarchyItem {
	return HierarchyItem{
		Name:   item.Name,
		Detail: derefString(item.Detail),
		Location: Location{
			File:     item.URI.FsPath(),
			Position: fromLSPPosition(item.Range.Start),
		},
	}
}

func typeHierarchyItemToHierarchyItem(item protocol.TypeHierarchyItem) HierarchyItem {
	return HierarchyItem{
		Name:   item.Name,
		Detail: derefString(item.Detail),
		Location: Location{
			File:     item.URI.FsPath(),
			Position: fromLSPPosition(item.Range.Start),
		},
	}
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// symbolKindName names the LSP SymbolKind values gopls actually emits for
// Go source; any other value degrades to "unknown" rather than leaking a
// raw protocol enum to callers.
func symbolKindName(kind protocol.SymbolKind) string {
	switch kind {
	case protocol.SymbolKindFile:
		return "file"
	case protocol.SymbolKindModule:
		return "module"
	case protocol.SymbolKindNamespace:
		return "namespace"
	case protocol.SymbolKindPackage:
		return "package"
	case protocol.SymbolKindClass:
		return "class"
	case protocol.SymbolKindMethod:
		return "method"
	case protocol.SymbolKindProperty:
		return "property"
	case protocol.SymbolKindField:
		return "field"
	case protocol.SymbolKindConstructor:
		return "constructor"
	case protocol.SymbolKindEnum:
		return "enum"
	case protocol.SymbolKindInterface:
		return "interface"
	case protocol.SymbolKindFunction:
		return "function"
	case protocol.SymbolKindVariable:
		return "variable"
	case protocol.SymbolKindConstant:
		return "constant"
	case protocol.SymbolKindStruct:
		return "struct"
	case protocol.SymbolKindTypeParameter:
		return "type_parameter"
	default:
		return "unknown"
	}
}
