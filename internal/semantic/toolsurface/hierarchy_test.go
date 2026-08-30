package toolsurface

import (
	"context"
	"testing"

	"github.com/Teagan42/forge/internal/semantic/gopls"
	"go.lsp.dev/protocol"
)

func hierarchyItemAt(name string, line int) gopls.HierarchyItem {
	return gopls.HierarchyItem{Name: name, Location: gopls.Location{File: "f.go", Position: gopls.Position{Line: line, Column: 1}}}
}

func TestCallHierarchy_IncomingReturnsCallers(t *testing.T) {
	driver := &fakeDriver{
		caps: protocol.ServerCapabilities{CallHierarchyProvider: protocol.Boolean(true)},
		callHierarchy: gopls.CallHierarchy{
			Item:    hierarchyItemAt("Target", 1),
			Callers: []gopls.HierarchyItem{hierarchyItemAt("CallerA", 5)},
			Callees: []gopls.HierarchyItem{hierarchyItemAt("CalleeB", 9)},
		},
	}
	ts := NewToolset(driver, Options{})

	got, err := ts.CallHierarchy(context.Background(), "f.go", 1, DirectionIncoming)
	if err != nil {
		t.Fatalf("CallHierarchy() error = %v", err)
	}
	if got.Total != 1 || got.Items[0].SymbolName != "CallerA" {
		t.Fatalf("got = %+v, want CallerA only", got)
	}
}

func TestCallHierarchy_OutgoingReturnsCallees(t *testing.T) {
	driver := &fakeDriver{
		caps: protocol.ServerCapabilities{CallHierarchyProvider: protocol.Boolean(true)},
		callHierarchy: gopls.CallHierarchy{
			Item:    hierarchyItemAt("Target", 1),
			Callers: []gopls.HierarchyItem{hierarchyItemAt("CallerA", 5)},
			Callees: []gopls.HierarchyItem{hierarchyItemAt("CalleeB", 9)},
		},
	}
	ts := NewToolset(driver, Options{})

	got, err := ts.CallHierarchy(context.Background(), "f.go", 1, DirectionOutgoing)
	if err != nil {
		t.Fatalf("CallHierarchy() error = %v", err)
	}
	if got.Total != 1 || got.Items[0].SymbolName != "CalleeB" {
		t.Fatalf("got = %+v, want CalleeB only", got)
	}
}

func TestCallHierarchy_UnsupportedCapabilityErrors(t *testing.T) {
	driver := &fakeDriver{}
	ts := NewToolset(driver, Options{})

	_, err := ts.CallHierarchy(context.Background(), "f.go", 1, DirectionIncoming)
	if err == nil {
		t.Fatalf("CallHierarchy() error = nil, want error (capability not advertised)")
	}
}

func TestTypeHierarchy_SuperReturnsSupertypes(t *testing.T) {
	driver := &fakeDriver{
		caps: protocol.ServerCapabilities{TypeHierarchyProvider: protocol.Boolean(true)},
		typeHierarchy: gopls.TypeHierarchy{
			Item:       hierarchyItemAt("Target", 1),
			Supertypes: []gopls.HierarchyItem{hierarchyItemAt("Base", 2)},
			Subtypes:   []gopls.HierarchyItem{hierarchyItemAt("Derived", 3)},
		},
	}
	ts := NewToolset(driver, Options{})

	got, err := ts.TypeHierarchy(context.Background(), "f.go", 1, DirectionSuper)
	if err != nil {
		t.Fatalf("TypeHierarchy() error = %v", err)
	}
	if got.Total != 1 || got.Items[0].SymbolName != "Base" {
		t.Fatalf("got = %+v, want Base only", got)
	}
}

func TestTypeHierarchy_UnsupportedCapabilityErrors(t *testing.T) {
	driver := &fakeDriver{}
	ts := NewToolset(driver, Options{})

	_, err := ts.TypeHierarchy(context.Background(), "f.go", 1, DirectionSub)
	if err == nil {
		t.Fatalf("TypeHierarchy() error = nil, want error (capability not advertised)")
	}
}

func TestCallHierarchy_UnknownDirectionErrors(t *testing.T) {
	driver := &fakeDriver{caps: protocol.ServerCapabilities{CallHierarchyProvider: protocol.Boolean(true)}}
	ts := NewToolset(driver, Options{})

	_, err := ts.CallHierarchy(context.Background(), "f.go", 1, Direction("bogus"))
	if err == nil {
		t.Fatalf("CallHierarchy() error = nil, want error for unknown direction")
	}
}
