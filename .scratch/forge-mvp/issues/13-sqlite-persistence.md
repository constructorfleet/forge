# 13 — SQLite persistence

**What to build:** Persist Execution and Issue state transactionally in SQLite. Schema covers executions, execution_issues, dependencies, workers, workspaces, agent_runs, gate_runs, pull_requests, ci_runs, and events. Every major state change creates an event. Storage is behind an interface so Postgres can be added later.

**Blocked by:** 11 — Project skeleton, domain model, and state machine

**Status:** resolved

- [x] Schema migrations create all tables
- [x] Execution can be created and reloaded with full state
- [x] Issue state transitions persist transactionally
- [x] Duplicate issue claims are prevented at the database level
- [x] Every state transition creates a timestamped event record
- [x] Event log is queryable by execution, issue, and time range
- [x] Storage interface abstracts SQLite — no SQL leaks into domain or orchestration code
- [x] Migration tests pass
- [x] Tests verify state survives simulated process restart (close and reopen DB)
