Status: resolved
Type: wayfinder:grilling

## Question

How does the CI Supervisor know which checks are required vs. optional?

## Answer

GitHub branch protection/rulesets are authoritative. The GitHub adapter exposes `GetMergeRequirements(baseBranch)` that normalizes whether requirements came from branch protection or rulesets. Optional check failures do not trigger CI repair. Config override available: `ci.required_checks.mode: explicit` with a check list, for repos without branch protection. MVP implements `github` and `explicit` modes only.
