# 20 — Review stage

**What to build:** After Quality Gates pass, invoke a fresh Agent for independent Review. The Reviewer receives only the diff, Issue requirements, Repository Context, and gate results — not the implementation Agent's conversation. Returns APPROVED (advance to COMMITTING) or CHANGES_REQUIRED with structured findings (severity, file, line, message) routed back to the implementation Worker.

**Blocked by:** 16 — Agent interface and fake adapter; 19 — Quality gate runner

**Status:** resolved

- [x] Review is a separate Agent invocation, not a continuation of the implementation session
- [x] Reviewer receives: diff (base...HEAD), issue requirements, repository policy, gate results
- [x] Reviewer does NOT receive implementation conversation history
- [x] APPROVED result transitions to COMMITTING
- [x] CHANGES_REQUIRED result includes structured findings with severity, file, line, message
- [x] CHANGES_REQUIRED findings route back to implementation Worker as feedback
- [x] Review invocations recorded in persistence
- [x] Integration test: gates pass → review APPROVED → state advances to COMMITTING
- [x] Integration test: gates pass → review CHANGES_REQUIRED → findings route to worker
