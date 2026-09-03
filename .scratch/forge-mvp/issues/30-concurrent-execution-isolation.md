# 30 — Concurrent execution isolation

**What to build:** Allow multiple Executions against the same repository with resource-scoped locks instead of a global mutex. Git metadata operations (fetch, worktree add/remove) take a short-lived repo lock. Each Issue takes an issue-level lock preventing two Executions from claiming it simultaneously. Branch publication takes a branch-level lock. Same Issue in two active Executions is disallowed by default.

**Blocked by:** 13 — SQLite persistence; 15 — Workspace manager; 26 — Multi-issue scheduling and concurrency

**Status:** resolved

- [x] Multiple Executions can run against the same repository
- [x] Git metadata lock: short-lived, serializes fetch/worktree add/worktree remove
- [x] Issue lock: prevents two Executions from implementing the same Issue
- [x] Branch lock: serializes publication operations on a specific branch
- [x] Worker implementation inside isolated Workspaces requires no lock
- [x] Attempting to claim an already-active Issue produces a clear error identifying the owning Execution
- [x] Each Execution has independent scheduler, states, and worktree paths
- [x] Lock contention does not deadlock (consistent acquisition order or timeout)
- [x] Integration test: two Executions with disjoint issues run concurrently
- [x] Integration test: two Executions claiming the same issue — second is rejected
