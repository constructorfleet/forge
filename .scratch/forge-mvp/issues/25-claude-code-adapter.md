# 25 — Claude Code adapter

**What to build:** Production Agent Adapter that invokes Claude Code in a Worker's Workspace with normalized Execution Context. Construct the prompt from issue context, repository context, and workflow policy. Parse structured results to distinguish IMPLEMENTED, NEEDS_INFO, and FAILED. Capture stdout/stderr and handle cancellation and exit codes.

**Blocked by:** 16 — Agent interface and fake adapter

**Status:** ready-for-agent

- [ ] Claude Code invoked as a subprocess in the Workspace directory
- [ ] Prompt constructed from Execution Context: issue, acceptance criteria, repository context, workflow policy, rules
- [ ] Agent instructed not to create PRs, manage labels, or decide workflow state
- [ ] Feedback (gate failures, review findings, CI diagnostics) included in prompt on retries
- [ ] IMPLEMENTED, NEEDS_INFO, and FAILED are distinguishable from agent output
- [ ] NEEDS_INFO result includes structured reason and questions
- [ ] stdout/stderr captured for diagnostics
- [ ] Cancellation support (context cancellation kills subprocess)
- [ ] Non-zero exit codes handled appropriately
- [ ] Environment sanitized — secrets excluded from agent context
