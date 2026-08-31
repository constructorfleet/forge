// Package planningagent implements the shared invocation mechanism Phase 2's
// planning contracts ride on: a typed structured-invocation core
// (InvokeStructured), a scripted fake Backend for deterministic tests, and
// the PlanningContext compiler that feeds agents typed, normalized data
// rather than raw Planning Artifact markdown. It knows nothing about any
// specific planning contract (goal drafting, decision grilling, spec
// drafting, ...); those are typed call sites built on top of this package by
// later tickets.
package planningagent

import "context"

// InvokeRequest is the input to Backend.Invoke: a prompt to run, identified
// for scripting/diagnostics by Key, and an optional JSON Schema describing
// the structured result the caller expects back. Schema travels with the
// request so a backend can pass it through to whatever native structured-
// output mechanism it has (or ignore it); InvokeStructured itself still
// extracts and decodes the result from fenced JSON in the raw output.
type InvokeRequest struct {
	Key    string
	Prompt string
	Schema []byte
}

// Backend is the minimal capability InvokeStructured needs from a coding
// Agent backend: given a request, return its raw text output. It is Phase 2's
// analogue of internal/agent.Agent's Execute -- planning contracts don't
// share Phase 1's fixed IMPLEMENTED/NEEDS_INFO/FAILED result shape, so they
// invoke a Backend directly and let InvokeStructured decode a
// contract-specific typed result from whatever fenced JSON the backend's
// output contains.
//
// req.Key identifies the invocation for a scripted test double (see
// FakeBackend); production backends ignore it.
type Backend interface {
	Invoke(ctx context.Context, req InvokeRequest) (string, error)
}
