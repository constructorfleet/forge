# 26 — Multi-issue scheduling and concurrency

**What to build:** Extend the execution engine from single-issue to multi-issue. `forge execute 345 344 343` resolves the dependency DAG, computes waves, executes independent Issues concurrently up to max parallel, and wakes the Scheduler on state changes. Dependency-blocked Issues wait for prerequisite PR merges, then start from a base containing the merged code.

**Blocked by:** 13 — SQLite persistence; 14 — GitHub tracker adapter; 18 — Single-issue execution engine

**Status:** resolved

- [x] Multiple issues accepted in `forge execute`
- [x] Dependency DAG resolved across all issues
- [x] Independent issues execute concurrently
- [x] Worker concurrency never exceeds configured max parallel
- [x] Dependency-blocked issues remain BLOCKED_DEPENDENCY until prerequisite PR merges
- [x] When prerequisite merges, dependent issue transitions to READY
- [x] Dependent issue's Worker base captured from current base branch (containing merged prerequisite code)
- [x] Scheduler wakes on meaningful state changes (issue completion, dependency satisfaction)
- [x] Duplicate scheduling does not occur (atomic READY → CLAIMED)
- [x] Integration test: independent issues run concurrently
- [x] Integration test: dependent issue waits for prerequisite, then starts from updated base
- [x] Integration test: max parallel respected
