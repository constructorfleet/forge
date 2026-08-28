# 13 — SQLite persistence

**What to build:** Persist Execution and Issue state transactionally in SQLite. Schema covers executions, execution_issues, dependencies, workers, workspaces, agent_runs, gate_runs, pull_requests, ci_runs, and events. Every major state change creates an event. Storage is behind an interface so Postgres can be added later.

**Blocked by:** 11 — Project skeleton, domain model, and state machine

**Status:** ready-for-agent

- [ ] Schema migrations create all tables
- [ ] Execution can be created and reloaded with full state
- [ ] Issue state transitions persist transactionally
- [ ] Duplicate issue claims are prevented at the database level
- [ ] Every state transition creates a timestamped event record
- [ ] Event log is queryable by execution, issue, and time range
- [ ] Storage interface abstracts SQLite — no SQL leaks into domain or orchestration code
- [ ] Migration tests pass
- [ ] Tests verify state survives simulated process restart (close and reopen DB)
