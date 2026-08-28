# Dependencies live in issue bodies, not in Forge config

Dependency metadata belongs in the issue tracker, not in `.forge.yaml`. Putting the dependency graph in repo config turns every ticket dependency into a config change — recreating an issue tracker with extra steps. The GitHub adapter parses a canonical `## Dependencies` block in the issue body with strict syntax (`Depends on: - #123`); no freeform NLP. Config overrides exist as an escape hatch (`dependencies.overrides` in `.forge.yaml`) with higher precedence, but the normal path is tracker-local.
