# 14 — GitHub tracker adapter and dependency parsing

**What to build:** Normalized Tracker Adapter for GitHub that fetches issues, parses comments, manages labels, and extracts dependency metadata. Parse the canonical `## Dependencies` block with strict syntax only — reject freeform text. Apply `.forge.yaml` dependency overrides (higher precedence). Construct a dependency DAG and detect cycles before any work begins. Expose `GetMergeRequirements` for CI required checks from branch protection/rulesets.

**Blocked by:** 11 — Project skeleton, domain model, and state machine; 12 — Configuration loading and validation

**Status:** ready-for-agent

- [ ] Fetch single and multiple issues, normalized to domain Issue type
- [ ] Fetch issue comments
- [ ] Add/remove labels idempotently
- [ ] Add comments
- [ ] Parse canonical `## Dependencies` syntax (`Depends on: - #123`)
- [ ] Accept `## Dependencies: None`
- [ ] Reject freeform dependency text (no NLP parsing)
- [ ] Apply config dependency overrides with correct precedence
- [ ] Construct DAG from parsed dependencies
- [ ] Detect cycles and produce useful error before workers launch
- [ ] `GetMergeRequirements` returns required checks from branch protection/rulesets
- [ ] Scheduler-facing code contains no GitHub-specific models
- [ ] Rate-limit errors surface clearly
- [ ] Tests use a mocked HTTP/API boundary
