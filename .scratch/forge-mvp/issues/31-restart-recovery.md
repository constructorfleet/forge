# 31 — Restart recovery

**What to build:** `forge resume <execution-id>` reconciles state after an orchestrator crash or termination. Validate worktrees, inspect process ownership, reconcile issue states, check open PR states, resume CI monitoring, and mark orphaned workers as recoverable. An orchestrator crash during any stage does not require re-creating the Execution.

**Blocked by:** 13 — SQLite persistence; 15 — Workspace manager; 22 — Commit and PR creation; 23 — CI supervisor

**Status:** resolved

- [x] Load incomplete Executions from persistence
- [x] Validate worktrees exist and are consistent
- [x] Reconcile issue states against actual worktree/branch/PR state
- [x] Reconcile open PR states against GitHub
- [x] Resume CI monitoring for issues in CI_PENDING
- [x] Orphaned active workers (no running process) marked recoverable
- [x] NEEDS_INFO issues: re-fetch comments and detect new human input
- [x] Recovery does not create duplicate PRs, branches, or commits
- [x] Crash during implementation → resume in existing worktree
- [x] Crash during CI wait → resume monitoring
- [x] Crash during PR creation → detect existing PR
- [x] Integration test: kill during implementation → resume → complete
- [x] Integration test: kill during CI_PENDING → resume → monitor → complete
