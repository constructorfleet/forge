# 15 — Workspace manager

**What to build:** Create and manage isolated Workspaces for Issue execution. Each Issue gets a worktree with an execution-scoped branch (`forge/<execution-id>/<issue>`). Base revision is captured when the Issue transitions to READY — dependency-blocked Issues start from a newer base containing prerequisite merged code. Primary checkout is never modified.

**Blocked by:** 11 — Project skeleton, domain model, and state machine; 12 — Configuration loading and validation

**Status:** ready-for-agent

- [ ] Worktree created at `.forge/worktrees/<execution-id>/<issue>/`
- [ ] Branch named `forge/<execution-id>/<issue>`
- [ ] Base revision captured per-Worker at READY transition, not at Execution start
- [ ] Primary checkout is never modified by workspace operations
- [ ] Existing worktrees handled idempotently (no error on re-creation)
- [ ] Worktree cleanup removes directory and Git worktree entry
- [ ] Recovery inspection can validate existing worktrees
- [ ] Git failures produce actionable error messages
- [ ] Tests use temporary Git repositories
