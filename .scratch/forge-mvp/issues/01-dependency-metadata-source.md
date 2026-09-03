Status: resolved
Type: wayfinder:grilling

## Question

Where does Dependency metadata live — in `.forge.yaml`, in the issue tracker, or both?

## Answer

Tracker-local by default. The GitHub adapter parses a canonical `## Dependencies` block in the issue body with strict syntax only (`Depends on: - #123`). No freeform NLP. Config overrides exist in `.forge.yaml` under `dependencies.overrides` with higher precedence, but primary storage stays in the tracker. See ADR 0003.
