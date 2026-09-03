Status: resolved
Type: wayfinder:grilling

## Question

Can two Executions target the same repository simultaneously? If so, how are branch name collisions and Workspace conflicts handled?

## Answer

Multiple Executions are allowed. Resource-scoped locks instead of a repo-wide mutex:
- **Git metadata lock**: short-lived, for fetch/worktree add/remove
- **Issue lock**: prevents two Executions from implementing the same Issue
- **Branch lock**: protects publication operations on a specific branch

Branches include Execution identity for uniqueness: `forge/e-123/345`. Worktrees are scoped: `.forge/worktrees/e-123/345/`. Worker implementation inside isolated Workspaces needs no lock. Same Issue in two active Executions is disallowed by default.

Base revision refinement: Worker base is captured when the Issue transitions to READY, not at Execution start. A dependency-blocked Issue starts from a newer base containing its prerequisite's merged code. See ADRs 0006, 0009.
