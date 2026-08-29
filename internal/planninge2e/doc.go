// Package planninge2e holds Forge's end-to-end golden scenarios for the
// Phase 2 planning compiler (ticket 23): goal -> Decisions -> Spec ->
// TicketPlan -> human approval -> Issue materialization -> Phase 1
// execution handoff, driven entirely through the deterministic fake
// structured backend (internal/planningagent.FakeBackend) and the in-memory
// tracker (internal/tracker.FakeTracker).
//
// It deliberately contains no production code: every package in the
// pipeline already has its own focused unit tests, and this package exists
// to prove they compose. Each scenario is one Test function rather than a
// shared table, because the setup a scenario needs (which invocation keys
// are scripted, which runtime state exists, which artifacts are approved)
// differs enough per branch that a table would hide more than it shares.
//
// The scenarios live in the external test package planninge2e_test, mirroring
// the black-box convention internal/specengine's tests use.
package planninge2e
