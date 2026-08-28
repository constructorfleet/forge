# 33 — Telemetry and token accounting

**What to build:** Capture execution telemetry to measure whether Forge actually reduces agent overhead. Track agent invocations, token usage (when the backend exposes it), retry counts per category, gate runtimes, issue cycle time, CI repair attempts, and context sizes. Expose via `forge status` summary and structured log events.

**Blocked by:** 13 — SQLite persistence; 16 — Agent interface and fake adapter; 19 — Quality gate runner; 23 — CI supervisor

**Status:** ready-for-agent

- [ ] Agent invocation count tracked per issue and per execution
- [ ] Token usage (input/output) captured when backend reports it
- [ ] Gate, review, and CI retry counts tracked separately
- [ ] Gate runtime (start/end) captured per gate per run
- [ ] Issue cycle time tracked (READY → DONE wall clock)
- [ ] Context size (bytes sent to agent) captured per invocation
- [ ] Structured log events include: execution_id, issue_id, worker_id, state, event, duration, agent backend
- [ ] Execution summary reportable: issues completed, invocations, tokens, retries, duration
- [ ] Integration test: execution produces non-zero telemetry for all tracked dimensions
