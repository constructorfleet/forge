Status: resolved
Type: wayfinder:grilling

## Question

Is `forge init` MVP, and if so, what does it do?

## Answer

Yes, MVP. Sharply scoped to deterministic repository-policy discovery and `.forge.yaml` generation. Detects base branch, package/build system, test/lint/format/typecheck/build commands, AGENTS.md/CLAUDE.md, tracker type. Priority: explicit known config formats → CI workflow inspection → conventional defaults → leave unresolved fields clearly marked. No LLM involvement. Must not modify issues, create labels, or configure branch protection.
