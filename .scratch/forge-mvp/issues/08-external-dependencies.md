Status: resolved
Type: wayfinder:grilling

## Question

If an Issue in the Execution set depends on an Issue outside the set, what happens?

## Answer

External dependencies are loaded into the DAG as observed nodes — tracked for satisfaction but never executed. Forge does not automatically add them to the Execution set. Satisfaction is checked by verifying merged code is reachable from the applicable base. Closed ≠ satisfied — issues get closed for many reasons. External nodes use states: EXTERNAL_PENDING, EXTERNAL_SATISFIED, EXTERNAL_INVALID. Managed dependents remain BLOCKED_DEPENDENCY until external prerequisites are satisfied. See ADR 0008.
