# A dependent Issue's base is stacked on its Dependencies' branches, not gitBase

ADR 0005 deferred "the stacked strategy (branching from prerequisite branches)" in favor of always resolving `cfg.Git.Base`'s current tip. That deferral is superseded for Managed Dependencies (issue #108): when an Issue has one or more Dependencies within the same `forge execute` invocation's requested set, its Worker's base is no longer `cfg.Git.Base`'s tip — it is derived from those Dependencies' own resulting branches, so the dependent's Workspace already contains their committed work before its Agent ever runs.

- A single Managed Dependency: the dependent's base is that Dependency's branch directly (`main -> issue/A -> issue/B`).
- More than one source (multiple Managed Dependencies, or a mix of Managed and External): the sources are merged into one synthetic `forge/integration/<issue>` branch (`workspace.Manager.Integrate`), recomputed from scratch on every resolution so it never serves a stale merge. A conflict between Dependencies aborts and reports a `*workspace.ConflictError` naming the offending branch and paths — Forge never silently drops one Dependency's changes or guesses a resolution.
- An Issue with no Dependencies, or only External ones (ADR 0008), is unaffected: it still resolves `cfg.Git.Base`'s current tip, exactly as before.

ADR 0005's "satisfied means merged" gating rule (`completionResolver.Satisfied`) is unchanged by this: *when* a dependent becomes runnable still turns on its Dependencies reaching a locally-successful state (or, for External Issues, actually merged and reachable). This ADR only changes *what repository state* a dependent's Worker is handed once it is runnable — the DAG now governs both, per issue #108's design principle.
