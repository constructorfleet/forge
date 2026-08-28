# 32 — Operational CLI

**What to build:** User-facing CLI commands for monitoring and controlling Executions. `forge status [execution-id]` shows issue states, workers, dependencies, PRs, and failures in a formatted table. `forge cancel <execution-id>` cleanly aborts an Execution. `forge retry <issue-execution-id>` reruns a failed Issue.

**Blocked by:** 13 — SQLite persistence; 23 — CI supervisor; 26 — Multi-issue scheduling and concurrency

**Status:** ready-for-agent

- [ ] `forge status` lists all active Executions with summary state
- [ ] `forge status <execution-id>` shows per-issue detail: state, worker, PR URL, failure info
- [ ] Status output includes dependency relationships
- [ ] `forge cancel <execution-id>` stops workers, preserves state, transitions active issues to CANCELLED
- [ ] `forge cancel` does not corrupt persistence or leave orphaned worktrees
- [ ] `forge retry <issue-execution-id>` transitions a FAILED issue back to READY
- [ ] `forge retry` on a non-FAILED issue produces a clear error
- [ ] Output is human-readable with aligned columns
- [ ] Integration test: execute → status shows progress → cancel → status shows CANCELLED
