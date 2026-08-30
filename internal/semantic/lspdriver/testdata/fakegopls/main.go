// Command fakegopls is a minimal stdio LSP server used only by
// internal/semantic/lspdriver's tests to exercise Driver's subprocess wiring,
// handshake, crash-restart, readiness-timeout, and per-capability query
// paths without depending on a real gopls binary being installed.
//
// Behavior is selected by the FAKEGOPLS_MODE environment variable:
//
//   - "normal" (default): completes initialize/initialized advertising a
//     fixed capability set, then serves query requests with hardcoded
//     responses keyed off the request position (see queryFixture below),
//     matching testdata/fixture/greet.go's known layout.
//   - "crash-after-init": completes initialize/initialized, then exits(1)
//     immediately afterward, simulating a server crash.
//   - "hang": never responds to initialize, simulating a wedged server so
//     the driver's readiness timeout fires.
//
// If FAKEGOPLS_SPAWN_LOG names a file, one line is appended to it on every
// invocation so a test can assert how many times the fixture process was
// launched. If FAKEGOPLS_OPEN_LOG names a file, one line (the opened URI)
// is appended to it on every textDocument/didOpen, so a test can assert a
// file was opened at most once across several query calls. If
// FAKEGOPLS_INIT_OPTIONS_LOG names a file, the raw initializationOptions
// JSON received on initialize (or "null" if none was sent) is appended to
// it, so a test can assert a ServerProfile's InitOptions were sent.
package main

import (
	"context"
	"os"
	"strings"
	"sync"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

func main() {
	if logPath := os.Getenv("FAKEGOPLS_SPAWN_LOG"); logPath != "" {
		appendLine(logPath, "spawn")
	}

	mode := os.Getenv("FAKEGOPLS_MODE")
	if mode == "hang" {
		select {} // never respond; the driver's readiness timeout must fire.
	}

	srv := &fakeServer{
		crashAfterInit: mode == "crash-after-init",
		openLog:        os.Getenv("FAKEGOPLS_OPEN_LOG"),
		initOptionsLog: os.Getenv("FAKEGOPLS_INIT_OPTIONS_LOG"),
	}
	stream := jsonrpc2.NewStream(stdio{})
	ctx, conn, _ := protocol.NewServer(context.Background(), srv, stream)
	_ = ctx

	<-conn.Done()
}

func appendLine(path, line string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = f.WriteString(line + "\n")
	_ = f.Close()
}

// stdio adapts the fixture process's own standard streams to
// io.ReadWriteCloser for jsonrpc2.NewStream.
type stdio struct{}

func (stdio) Read(p []byte) (int, error)  { return os.Stdin.Read(p) }
func (stdio) Write(p []byte) (int, error) { return os.Stdout.Write(p) }
func (stdio) Close() error {
	_ = os.Stdin.Close()
	return os.Stdout.Close()
}

type fakeServer struct {
	protocol.UnimplementedServer

	crashAfterInit bool
	openLog        string
	initOptionsLog string

	mu     sync.Mutex
	opened map[string]int
}

func (s *fakeServer) Initialize(_ context.Context, params *protocol.InitializeParams) (*protocol.InitializeResult, error) {
	if s.initOptionsLog != "" {
		raw := params.InitializationOptions
		if raw == nil {
			appendLine(s.initOptionsLog, "null")
		} else {
			appendLine(s.initOptionsLog, string(raw))
		}
	}
	return &protocol.InitializeResult{
		Capabilities: protocol.ServerCapabilities{
			HoverProvider:           protocol.Boolean(true),
			DefinitionProvider:      protocol.Boolean(true),
			ReferencesProvider:      protocol.Boolean(true),
			ImplementationProvider:  protocol.Boolean(true),
			DocumentSymbolProvider:  protocol.Boolean(true),
			WorkspaceSymbolProvider: protocol.Boolean(true),
			CallHierarchyProvider:   protocol.Boolean(true),
			TypeHierarchyProvider:   protocol.Boolean(true),
		},
	}, nil
}

func (s *fakeServer) Initialized(context.Context, *protocol.InitializedParams) error {
	if s.crashAfterInit {
		os.Exit(1)
	}
	return nil
}

func (s *fakeServer) Shutdown(context.Context) error {
	return nil
}

func (s *fakeServer) Exit(context.Context) error {
	os.Exit(0)
	return nil
}

// DidOpen records the opened URI (for lazy-open assertions) and, if
// FAKEGOPLS_OPEN_LOG is set, appends it there too.
func (s *fakeServer) DidOpen(_ context.Context, params *protocol.DidOpenTextDocumentParams) error {
	s.mu.Lock()
	if s.opened == nil {
		s.opened = make(map[string]int)
	}
	s.opened[string(params.TextDocument.URI)]++
	s.mu.Unlock()

	if s.openLog != "" {
		appendLine(s.openLog, string(params.TextDocument.URI))
	}
	return nil
}

// The Definition/References/Implementation/Hover/DocumentSymbol/Symbols/
// call- and type-hierarchy handlers below are hardcoded against
// testdata/fixture/greet.go's known layout (see that file's doc comment):
// they key off the request's line/character rather than reading the file,
// so a test asserting the returned file:line is checking Driver's own
// position conversion and result mapping, not an echo of what it sent.

const fixtureURI = "greet.go" // matched via strings.HasSuffix against the request URI.

var (
	greetDefLoc = protocol.Location{
		Range: protocol.Range{
			Start: protocol.Position{Line: 15, Character: 5},
			End:   protocol.Position{Line: 15, Character: 10},
		},
	}
	callerDefLoc = protocol.Location{
		Range: protocol.Range{
			Start: protocol.Position{Line: 20, Character: 5},
			End:   protocol.Position{Line: 20, Character: 11},
		},
	}
	englishGreeterDefLoc = protocol.Location{
		Range: protocol.Range{
			Start: protocol.Position{Line: 12, Character: 5},
			End:   protocol.Position{Line: 12, Character: 19},
		},
	}
	greeterDefLoc = protocol.Location{
		Range: protocol.Range{
			Start: protocol.Position{Line: 7, Character: 5},
			End:   protocol.Position{Line: 7, Character: 12},
		},
	}
)

func withURI(loc protocol.Location, docURI uri.URI) protocol.Location {
	loc.URI = docURI
	return loc
}

func (s *fakeServer) Definition(_ context.Context, params *protocol.DefinitionParams) (protocol.DefinitionResult, error) {
	if !strings.HasSuffix(string(params.TextDocument.URI), fixtureURI) {
		return nil, nil
	}
	// Definition queries the tests issue: from the Greet() call at line 22
	// (0-based 21), and from Greet's own definition at line 16 (0-based
	// 15) — both resolve to Greet's definition.
	switch params.Position.Line {
	case 21, 15:
		loc := withURI(greetDefLoc, params.TextDocument.URI)
		return &loc, nil
	}
	return nil, nil
}

func (s *fakeServer) References(_ context.Context, params *protocol.ReferenceParams) ([]protocol.Location, error) {
	if !strings.HasSuffix(string(params.TextDocument.URI), fixtureURI) {
		return nil, nil
	}
	// References to Greet, queried from its own definition (0-based line 15).
	if params.Position.Line == 15 {
		return []protocol.Location{
			withURI(greetDefLoc, params.TextDocument.URI),
			{
				URI: params.TextDocument.URI,
				Range: protocol.Range{
					Start: protocol.Position{Line: 21, Character: 8},
					End:   protocol.Position{Line: 21, Character: 13},
				},
			},
		}, nil
	}
	return nil, nil
}

func (s *fakeServer) Implementation(_ context.Context, params *protocol.ImplementationParams) (protocol.DefinitionResult, error) {
	if !strings.HasSuffix(string(params.TextDocument.URI), fixtureURI) {
		return nil, nil
	}
	// Implementations of Greeter (0-based line 7) is EnglishGreeter.
	if params.Position.Line == 7 {
		return protocol.LocationSlice{withURI(englishGreeterDefLoc, params.TextDocument.URI)}, nil
	}
	return nil, nil
}

func (s *fakeServer) Hover(_ context.Context, params *protocol.HoverParams) (*protocol.Hover, error) {
	if !strings.HasSuffix(string(params.TextDocument.URI), fixtureURI) {
		return nil, nil
	}
	if params.Position.Line == 15 {
		return &protocol.Hover{
			Contents: &protocol.MarkupContent{
				Kind:  protocol.MarkupKindMarkdown,
				Value: "```go\nfunc Greet(name string) string\n```\n\nGreet returns an English greeting for name.",
			},
		}, nil
	}
	return nil, nil
}

func (s *fakeServer) DocumentSymbol(_ context.Context, params *protocol.DocumentSymbolParams) (protocol.DocumentSymbolResult, error) {
	if !strings.HasSuffix(string(params.TextDocument.URI), fixtureURI) {
		return nil, nil
	}
	return protocol.DocumentSymbolSlice{
		{
			Name:           "Greeter",
			Kind:           protocol.SymbolKindInterface,
			Range:          protocol.Range{Start: protocol.Position{Line: 7, Character: 0}, End: protocol.Position{Line: 9, Character: 1}},
			SelectionRange: protocol.Range{Start: protocol.Position{Line: 7, Character: 5}, End: protocol.Position{Line: 7, Character: 12}},
		},
		{
			Name:           "EnglishGreeter",
			Kind:           protocol.SymbolKindStruct,
			Range:          protocol.Range{Start: protocol.Position{Line: 12, Character: 0}, End: protocol.Position{Line: 12, Character: 29}},
			SelectionRange: protocol.Range{Start: protocol.Position{Line: 12, Character: 5}, End: protocol.Position{Line: 12, Character: 19}},
		},
		{
			Name:           "Greet",
			Kind:           protocol.SymbolKindFunction,
			Range:          protocol.Range{Start: protocol.Position{Line: 15, Character: 0}, End: protocol.Position{Line: 17, Character: 1}},
			SelectionRange: protocol.Range{Start: protocol.Position{Line: 15, Character: 5}, End: protocol.Position{Line: 15, Character: 10}},
		},
		{
			Name:           "Caller",
			Kind:           protocol.SymbolKindFunction,
			Range:          protocol.Range{Start: protocol.Position{Line: 20, Character: 0}, End: protocol.Position{Line: 22, Character: 1}},
			SelectionRange: protocol.Range{Start: protocol.Position{Line: 20, Character: 5}, End: protocol.Position{Line: 20, Character: 11}},
		},
	}, nil
}

func (s *fakeServer) Symbols(_ context.Context, params *protocol.WorkspaceSymbolParams) (protocol.WorkspaceSymbolResult, error) {
	return protocol.SymbolInformationSlice{
		{
			BaseSymbolInformation: protocol.BaseSymbolInformation{Name: "Greet", Kind: protocol.SymbolKindFunction},
			Location:              greetDefLoc,
		},
		{
			BaseSymbolInformation: protocol.BaseSymbolInformation{Name: "Caller", Kind: protocol.SymbolKindFunction},
			Location:              callerDefLoc,
		},
	}, nil
}

func (s *fakeServer) PrepareCallHierarchy(_ context.Context, params *protocol.CallHierarchyPrepareParams) ([]protocol.CallHierarchyItem, error) {
	if !strings.HasSuffix(string(params.TextDocument.URI), fixtureURI) {
		return nil, nil
	}
	if params.Position.Line == 20 {
		return []protocol.CallHierarchyItem{callerHierarchyItem(params.TextDocument.URI)}, nil
	}
	return nil, nil
}

func (s *fakeServer) IncomingCalls(_ context.Context, params *protocol.CallHierarchyIncomingCallsParams) ([]protocol.CallHierarchyIncomingCall, error) {
	if params.Item.Name == "Caller" {
		return nil, nil // nothing in the fixture calls Caller.
	}
	return nil, nil
}

func (s *fakeServer) OutgoingCalls(_ context.Context, params *protocol.CallHierarchyOutgoingCallsParams) ([]protocol.CallHierarchyOutgoingCall, error) {
	if params.Item.Name == "Caller" {
		return []protocol.CallHierarchyOutgoingCall{
			{
				To:         greetHierarchyItem(params.Item.URI),
				FromRanges: []protocol.Range{{Start: protocol.Position{Line: 21, Character: 8}, End: protocol.Position{Line: 21, Character: 13}}},
			},
		}, nil
	}
	return nil, nil
}

func (s *fakeServer) PrepareTypeHierarchy(_ context.Context, params *protocol.TypeHierarchyPrepareParams) ([]protocol.TypeHierarchyItem, error) {
	if !strings.HasSuffix(string(params.TextDocument.URI), fixtureURI) {
		return nil, nil
	}
	if params.Position.Line == 12 {
		return []protocol.TypeHierarchyItem{englishGreeterHierarchyItem(params.TextDocument.URI)}, nil
	}
	return nil, nil
}

func (s *fakeServer) Supertypes(_ context.Context, params *protocol.TypeHierarchySupertypesParams) ([]protocol.TypeHierarchyItem, error) {
	if params.Item.Name == "EnglishGreeter" {
		return []protocol.TypeHierarchyItem{greeterHierarchyItem(params.Item.URI)}, nil
	}
	return nil, nil
}

func (s *fakeServer) Subtypes(context.Context, *protocol.TypeHierarchySubtypesParams) ([]protocol.TypeHierarchyItem, error) {
	return nil, nil
}

func callerHierarchyItem(docURI uri.URI) protocol.CallHierarchyItem {
	return protocol.CallHierarchyItem{
		Name:           "Caller",
		Kind:           protocol.SymbolKindFunction,
		URI:            docURI,
		Range:          callerDefLoc.Range,
		SelectionRange: callerDefLoc.Range,
	}
}

func greetHierarchyItem(docURI uri.URI) protocol.CallHierarchyItem {
	return protocol.CallHierarchyItem{
		Name:           "Greet",
		Kind:           protocol.SymbolKindFunction,
		URI:            docURI,
		Range:          greetDefLoc.Range,
		SelectionRange: greetDefLoc.Range,
	}
}

func englishGreeterHierarchyItem(docURI uri.URI) protocol.TypeHierarchyItem {
	return protocol.TypeHierarchyItem{
		Name:           "EnglishGreeter",
		Kind:           protocol.SymbolKindStruct,
		URI:            docURI,
		Range:          englishGreeterDefLoc.Range,
		SelectionRange: englishGreeterDefLoc.Range,
	}
}

func greeterHierarchyItem(docURI uri.URI) protocol.TypeHierarchyItem {
	return protocol.TypeHierarchyItem{
		Name:           "Greeter",
		Kind:           protocol.SymbolKindInterface,
		URI:            docURI,
		Range:          greeterDefLoc.Range,
		SelectionRange: greeterDefLoc.Range,
	}
}
