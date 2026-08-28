# 20 — Review stage

**What to build:** After Quality Gates pass, invoke a fresh Agent for independent Review. The Reviewer receives only the diff, Issue requirements, Repository Context, and gate results — not the implementation Agent's conversation. Returns APPROVED (advance to COMMITTING) or CHANGES_REQUIRED with structured findings (severity, file, line, message) routed back to the implementation Worker.

**Blocked by:** 16 — Agent interface and fake adapter; 19 — Quality gate runner

**Status:** ready-for-agent

- [ ] Review is a separate Agent invocation, not a continuation of the implementation session
- [ ] Reviewer receives: diff (base...HEAD), issue requirements, repository policy, gate results
- [ ] Reviewer does NOT receive implementation conversation history
- [ ] APPROVED result transitions to COMMITTING
- [ ] CHANGES_REQUIRED result includes structured findings with severity, file, line, message
- [ ] CHANGES_REQUIRED findings route back to implementation Worker as feedback
- [ ] Review invocations recorded in persistence
- [ ] Integration test: gates pass → review APPROVED → state advances to COMMITTING
- [ ] Integration test: gates pass → review CHANGES_REQUIRED → findings route to worker
