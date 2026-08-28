# 28 — Needs-info flow

**What to build:** When an Agent returns NEEDS_INFO, the orchestrator adds the configured label, posts a structured comment with the question and relevant context, preserves the Workspace, releases the Worker slot, and transitions to NEEDS_INFO. `forge resume` re-fetches the issue comments, detects new human input since the checkpoint, and transitions NEEDS_INFO → READY with focused context (original issue + previous question + new comments only).

**Blocked by:** 14 — GitHub tracker adapter; 18 — Single-issue execution engine

**Status:** resolved

- [x] NEEDS_INFO result triggers: configured label added, structured comment posted
- [x] Comment includes: question, reason, brief relevant context
- [x] Workspace preserved (worktree remains)
- [x] Worker slot released (other ready work can proceed)
- [x] Needs-info checkpoint persisted (questions, timestamp, comment state)
- [x] `forge resume` re-fetches issue comments
- [x] New human comments since checkpoint detected
- [x] NEEDS_INFO → READY transition on new human input
- [x] Resumed Worker receives: original issue context + previous question + new comments only (not full history)
- [x] No PR created for NEEDS_INFO issues
- [x] Label and comment operations are idempotent
- [x] Integration test: agent NEEDS_INFO → label + comment → resume with new input → READY
