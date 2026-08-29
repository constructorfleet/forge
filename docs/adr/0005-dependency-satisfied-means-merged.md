# A dependency is satisfied only when its PR is merged

The scheduler considers a dependency satisfied when the prerequisite issue's PR is merged into the execution base branch — not when Forge marks the implementation locally complete, and not when CI goes green. This keeps "ready" tied to code that actually exists in the base lineage and avoids inventing accidental stacked-branch semantics. The stacked strategy (branching from prerequisite branches) is explicitly deferred.

> Superseded in part by [ADR 0012](0012-dependent-base-is-stacked-on-dependency-branches.md): the stacked strategy this ADR deferred is now implemented for Managed Dependencies' *base construction*. This ADR's "satisfied means merged" *gating* rule is otherwise unchanged.
