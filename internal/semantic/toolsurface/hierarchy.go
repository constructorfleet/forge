package toolsurface

import (
	"context"
	"fmt"

	"github.com/Teagan42/forge/internal/semantic/gopls"
)

// Direction selects which side of a hierarchy call_hierarchy/type_hierarchy
// returns.
type Direction string

const (
	// DirectionIncoming returns call_hierarchy's callers.
	DirectionIncoming Direction = "incoming"
	// DirectionOutgoing returns call_hierarchy's callees.
	DirectionOutgoing Direction = "outgoing"
	// DirectionSuper returns type_hierarchy's supertypes.
	DirectionSuper Direction = "super"
	// DirectionSub returns type_hierarchy's subtypes.
	DirectionSub Direction = "sub"
)

// CallHierarchy resolves the symbol at (file, line) and returns its callers
// (DirectionIncoming) or callees (DirectionOutgoing), flattened to
// location-only Source Locations and capped at t.maxResults. Gated behind
// the Driver's CallHierarchyProvider capability (see ADR-0014): a Driver
// that doesn't advertise it returns gopls.ErrCapabilityUnsupported.
func (t *Toolset) CallHierarchy(ctx context.Context, file string, line int, direction Direction) (ListResult, error) {
	if !providerEnabled(t.driver.Capabilities().CallHierarchyProvider) {
		return ListResult{}, fmt.Errorf("toolsurface: call_hierarchy: %w", gopls.ErrCapabilityUnsupported)
	}

	pos, err := resolvePosition(ctx, t.driver, file, line, "")
	if err != nil {
		return ListResult{}, err
	}

	result, err := t.driver.CallHierarchy(ctx, file, pos)
	if err != nil {
		return ListResult{}, err
	}

	var items []gopls.HierarchyItem
	switch direction {
	case DirectionIncoming:
		items = result.Callers
	case DirectionOutgoing:
		items = result.Callees
	default:
		return ListResult{}, fmt.Errorf("toolsurface: unsupported call_hierarchy direction %q; supported: %s, %s", direction, DirectionIncoming, DirectionOutgoing)
	}

	return capLocations(hierarchyLocations(items), t.maxResults), nil
}

// TypeHierarchy resolves the symbol at (file, line) and returns its
// supertypes (DirectionSuper) or subtypes (DirectionSub), flattened to
// location-only Source Locations and capped at t.maxResults. Gated behind
// the Driver's TypeHierarchyProvider capability (see ADR-0014).
func (t *Toolset) TypeHierarchy(ctx context.Context, file string, line int, direction Direction) (ListResult, error) {
	if !providerEnabled(t.driver.Capabilities().TypeHierarchyProvider) {
		return ListResult{}, fmt.Errorf("toolsurface: type_hierarchy: %w", gopls.ErrCapabilityUnsupported)
	}

	pos, err := resolvePosition(ctx, t.driver, file, line, "")
	if err != nil {
		return ListResult{}, err
	}

	result, err := t.driver.TypeHierarchy(ctx, file, pos)
	if err != nil {
		return ListResult{}, err
	}

	var items []gopls.HierarchyItem
	switch direction {
	case DirectionSuper:
		items = result.Supertypes
	case DirectionSub:
		items = result.Subtypes
	default:
		return ListResult{}, fmt.Errorf("toolsurface: unsupported type_hierarchy direction %q; supported: %s, %s", direction, DirectionSuper, DirectionSub)
	}

	return capLocations(hierarchyLocations(items), t.maxResults), nil
}

// hierarchyLocations normalizes []gopls.HierarchyItem into Source Locations,
// carrying each item's name.
func hierarchyLocations(items []gopls.HierarchyItem) []SourceLocation {
	locs := make([]SourceLocation, 0, len(items))
	for _, item := range items {
		loc := sourceLocationFromGoplsLocation(item.Location)
		loc.SymbolName = item.Name
		locs = append(locs, loc)
	}
	return locs
}
