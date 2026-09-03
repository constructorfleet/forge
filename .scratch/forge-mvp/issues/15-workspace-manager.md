# 15 — Workspace manager

**What to build:** Create and manage isolated Workspaces for Issue execution. Each Issue gets a worktree with an execution-scoped branch (`forge/<execution-id>/<issue>`). Base revision is captured when the Issue transitions to READY — dependency-blocked Issues start from a newer base containing prerequisite merged code. Primary checkout is never modified.

**Blocked by:** 11 — Project skeleton, domain model, and state machine; 12 — Configuration loading and validation

**Status:** resolved

- [x] Worktree created at `.forge/worktrees/<execution-id>/<issue>/`
- [x] Branch named `forge/<execution-id>/<issue>`
- [x] Base revision captured per-Worker at READY transition, not at Execution start
- [x] Primary checkout is never modified by workspace operations
- [x] Existing worktrees handled idempotently (no error on re-creation)
- [x] Worktree cleanup removes directory and Git worktree entry
- [x] Recovery inspection can validate existing worktrees
- [x] Git failures produce actionable error messages
- [x] Tests use temporary Git repositories
