# 12 — Configuration loading and validation

**What to build:** Load and validate `.forge.yaml` repository configuration with sensible defaults. Cover all sections: tracker, git (base, branch template, worktree root), execution (max parallel), retry budgets (separate gate/review/CI ceilings), workflow, quality gates, pull requests, CI (required checks mode), blocked-issue behavior, agent provider, and dependency overrides.

**Blocked by:** 11 — Project skeleton, domain model, and state machine

**Status:** ready-for-agent

- [ ] Valid `.forge.yaml` loads with all sections parsed
- [ ] Missing optional fields receive deterministic defaults
- [ ] Invalid fields produce useful error messages identifying the problem
- [ ] Partial configurations load successfully with defaults for missing sections
- [ ] Retry budget defaults: gates 3, review 2, CI 3
- [ ] CI required checks mode supports `github` and `explicit`
- [ ] Dependency overrides section parsed correctly
- [ ] Secrets are never expected or stored in config
- [ ] Tests cover malformed, partial, and complete configurations
