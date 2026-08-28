# 31 — Restart recovery

**What to build:** `forge resume <execution-id>` reconciles state after an orchestrator crash or termination. Validate worktrees, inspect process ownership, reconcile issue states, check open PR states, resume CI monitoring, and mark orphaned workers as recoverable. An orchestrator crash during any stage does not require re-creating the Execution.

**Blocked by:** 13 — SQLite persistence; 15 — Workspace manager; 22 — Commit and PR creation; 23 — CI supervisor

**Status:** ready-for-agent

- [ ] Load incomplete Executions from persistence
- [ ] Validate worktrees exist and are consistent
- [ ] Reconcile issue states against actual worktree/branch/PR state
- [ ] Reconcile open PR states against GitHub
- [ ] Resume CI monitoring for issues in CI_PENDING
- [ ] Orphaned active workers (no running process) marked recoverable
- [ ] NEEDS_INFO issues: re-fetch comments and detect new human input
- [ ] Recovery does not create duplicate PRs, branches, or commits
- [ ] Crash during implementation → resume in existing worktree
- [ ] Crash during CI wait → resume monitoring
- [ ] Crash during PR creation → detect existing PR
- [ ] Integration test: kill during implementation → resume → complete
- [ ] Integration test: kill during CI_PENDING → resume → monitor → complete
