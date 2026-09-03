# 32 — Operational CLI

**What to build:** User-facing CLI commands for monitoring and controlling Executions. `forge status [execution-id]` shows issue states, workers, dependencies, PRs, and failures in a formatted table. `forge cancel <execution-id>` cleanly aborts an Execution. `forge retry <issue-execution-id>` reruns a failed Issue.

**Blocked by:** 13 — SQLite persistence; 23 — CI supervisor; 26 — Multi-issue scheduling and concurrency

**Status:** resolved

- [x] `forge status` lists all active Executions with summary state
- [x] `forge status <execution-id>` shows per-issue detail: state, worker, PR URL, failure info
- [x] Status output includes dependency relationships
- [x] `forge cancel <execution-id>` stops workers, preserves state, transitions active issues to CANCELLED
- [x] `forge cancel` does not corrupt persistence or leave orphaned worktrees
- [x] `forge retry <issue-execution-id>` transitions a FAILED issue back to READY
- [x] `forge retry` on a non-FAILED issue produces a clear error
- [x] Output is human-readable with aligned columns
- [x] Integration test: execute → status shows progress → cancel → status shows CANCELLED
