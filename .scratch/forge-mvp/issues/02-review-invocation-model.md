Status: resolved
Type: wayfinder:grilling

## Question

Is MVP review a self-review continuation within the implementation Worker's session, or a fresh Agent invocation?

## Answer

Fresh second Agent invocation. Same Workspace, but the Reviewer gets only the diff, Issue requirements, repo policy, and gate results — not the implementation conversation. This prevents the implementation Agent from grading its own homework and enables cross-backend review later (Claude implements, Codex reviews). See ADR 0004.
