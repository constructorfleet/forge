Status: resolved
Type: wayfinder:grilling

## Question

Does Review rejection share the implementation retry counter or have its own separate ceiling?

## Answer

Separate budgets. Gate failures, review rejections, and CI failures are different failure classes with independent configurable ceilings (e.g., gates: 3, review: 2, ci: 3). Every repair — regardless of trigger — must rerun the full quality gate set. A shared counter would let ordinary development churn (two lint mistakes + one reviewer objection) exhaust the budget. See ADR 0007.
