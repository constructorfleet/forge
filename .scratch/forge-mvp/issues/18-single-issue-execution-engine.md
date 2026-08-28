# 18 — Single-issue execution engine

**What to build:** The first end-to-end wiring of the orchestration pipeline for a single Issue with no dependencies. `forge execute <issue>` fetches a real GitHub issue, validates no cycles, creates a Workspace, compiles Execution Context, invokes the (fake) Agent, and persists all state transitions. This is the first demoable vertical slice — run it with the fake adapter and watch state flow from PENDING through IMPLEMENTING.

**Blocked by:** 13 — SQLite persistence; 14 — GitHub tracker adapter; 15 — Workspace manager; 16 — Agent interface and fake adapter; 17 — Repository Context compiler

**Status:** ready-for-agent

- [ ] `forge execute <issue-number>` creates an Execution, fetches the issue, and begins work
- [ ] Issue transitions through: PENDING → READY → CLAIMED → PREPARING → IMPLEMENTING
- [ ] Workspace is created before agent invocation
- [ ] Execution Context assembled from Repository Context + issue-specific data
- [ ] Agent invoked with correct Execution Context in the Workspace
- [ ] Agent IMPLEMENTED result advances state past IMPLEMENTING
- [ ] All state transitions persisted in SQLite with events
- [ ] Execution can be inspected after completion (basic `forge status`)
- [ ] Integration test: single issue with fake agent reaches IMPLEMENTED state
