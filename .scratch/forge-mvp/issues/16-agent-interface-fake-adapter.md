# 16 — Agent interface and fake adapter

**What to build:** Backend-independent Agent contract with `Execute(ctx, AgentRequest) → AgentResult`. Define request (workspace path, issue context, repository context, workflow policy, feedback) and result (status, summary, needs-info detail) types. Build a fake deterministic adapter that can be configured per-test to return IMPLEMENTED, NEEDS_INFO, or FAILED with controllable behavior.

**Blocked by:** 11 — Project skeleton, domain model, and state machine

**Status:** resolved

- [x] Agent interface defined with `Execute` method
- [x] AgentRequest carries workspace path, issue context, repository context, workflow policy, and feedback
- [x] AgentResult carries status (IMPLEMENTED / NEEDS_INFO / FAILED), summary, and structured needs-info detail
- [x] Fake adapter supports configurable outcomes per scenario
- [x] Fake adapter supports success, failure, and needs-info scenarios
- [x] Fake adapter records invocations for test assertions
- [x] Orchestrator code has no Claude or Codex dependencies
- [x] Scheduler integration tests can use fake adapter
