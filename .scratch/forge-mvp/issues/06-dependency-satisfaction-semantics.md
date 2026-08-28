Status: resolved
Type: wayfinder:grilling

## Question

When is a Dependency considered satisfied — locally complete, CI green, or PR merged?

## Answer

PR merged into the Execution's base branch. This keeps the Scheduler's meaning of "ready" tied to code that actually exists in the base lineage and avoids inventing accidental stacked-branch semantics. See ADR 0005.
