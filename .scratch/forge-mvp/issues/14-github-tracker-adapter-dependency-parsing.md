# 14 — GitHub tracker adapter and dependency parsing

**What to build:** Normalized Tracker Adapter for GitHub that fetches issues, parses comments, manages labels, and extracts dependency metadata. Parse the canonical `## Dependencies` block with strict syntax only — reject freeform text. Apply `.forge.yaml` dependency overrides (higher precedence). Construct a dependency DAG and detect cycles before any work begins. Expose `GetMergeRequirements` for CI required checks from branch protection/rulesets.

**Blocked by:** 11 — Project skeleton, domain model, and state machine; 12 — Configuration loading and validation

**Status:** resolved

- [x] Fetch single and multiple issues, normalized to domain Issue type
- [x] Fetch issue comments
- [x] Add/remove labels idempotently
- [x] Add comments
- [x] Parse canonical `## Dependencies` syntax (`Depends on: - #123`)
- [x] Accept `## Dependencies: None`
- [x] Reject freeform dependency text (no NLP parsing)
- [x] Apply config dependency overrides with correct precedence
- [x] Construct DAG from parsed dependencies
- [x] Detect cycles and produce useful error before workers launch
- [x] `GetMergeRequirements` returns required checks from branch protection/rulesets
- [x] Scheduler-facing code contains no GitHub-specific models
- [x] Rate-limit errors surface clearly
- [x] Tests use a mocked HTTP/API boundary
