# 16 — Agent interface and fake adapter

**What to build:** Backend-independent Agent contract with `Execute(ctx, AgentRequest) → AgentResult`. Define request (workspace path, issue context, repository context, workflow policy, feedback) and result (status, summary, needs-info detail) types. Build a fake deterministic adapter that can be configured per-test to return IMPLEMENTED, NEEDS_INFO, or FAILED with controllable behavior.

**Blocked by:** 11 — Project skeleton, domain model, and state machine

**Status:** ready-for-agent

- [ ] Agent interface defined with `Execute` method
- [ ] AgentRequest carries workspace path, issue context, repository context, workflow policy, and feedback
- [ ] AgentResult carries status (IMPLEMENTED / NEEDS_INFO / FAILED), summary, and structured needs-info detail
- [ ] Fake adapter supports configurable outcomes per scenario
- [ ] Fake adapter supports success, failure, and needs-info scenarios
- [ ] Fake adapter records invocations for test assertions
- [ ] Orchestrator code has no Claude or Codex dependencies
- [ ] Scheduler integration tests can use fake adapter
