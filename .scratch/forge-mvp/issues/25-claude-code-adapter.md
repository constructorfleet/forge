# 25 — Claude Code adapter

**What to build:** Production Agent Adapter that invokes Claude Code in a Worker's Workspace with normalized Execution Context. Construct the prompt from issue context, repository context, and workflow policy. Parse structured results to distinguish IMPLEMENTED, NEEDS_INFO, and FAILED. Capture stdout/stderr and handle cancellation and exit codes.

**Blocked by:** 16 — Agent interface and fake adapter

**Status:** resolved

- [x] Claude Code invoked as a subprocess in the Workspace directory
- [x] Prompt constructed from Execution Context: issue, acceptance criteria, repository context, workflow policy, rules
- [x] Agent instructed not to create PRs, manage labels, or decide workflow state
- [x] Feedback (gate failures, review findings, CI diagnostics) included in prompt on retries
- [x] IMPLEMENTED, NEEDS_INFO, and FAILED are distinguishable from agent output
- [x] NEEDS_INFO result includes structured reason and questions
- [x] stdout/stderr captured for diagnostics
- [x] Cancellation support (context cancellation kills subprocess)
- [x] Non-zero exit codes handled appropriately
- [x] Environment sanitized — secrets excluded from agent context
