## Agent skills

### Issue tracker

Issues are tracked as GitHub issues on `constructorfleet/forge` (use the `gh` CLI).
Dependencies between issues go in a `## Dependencies` block in the issue body (the
canonical Dependency Source — see `CONTEXT.md`). The `.scratch/` directory is for
in-progress specs and wayfinding maps only, not the issue backlog.

### Handling issues

Implement a ready issue by driving it through Forge itself:
`forge execute <issue-number>`. Forge is the orchestrator this repo builds — dogfood it to
handle issues (implement → gates → PR) rather than editing by hand. Merge each prerequisite
before its dependents, and ensure the quality-gate binaries (notably `golangci-lint`) are on
`PATH`. See `docs/agents/handling-issues.md`.

### Triage labels

Default five-role vocabulary (needs-triage, needs-info, ready-for-agent, ready-for-human, wontfix). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context layout — one `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.

## Writing standard

Write all comments and documentation in ASD-STE100 Simplified Technical English
(STE). This applies to code comments, doc comments, `CONTEXT.md`, ADRs, docs under
`docs/`, and issue and PR text.

Follow the STE rules:
- Use short sentences (procedure sentences 20 words or fewer; descriptive sentences
  25 words or fewer). One instruction per sentence.
- Use the active voice and the present tense.
- Use approved words in their one approved meaning; keep one term for one thing (do
  not use synonyms for the same concept).
- Use simple, direct words; avoid jargon, idioms, and figurative language.
- Start each paragraph with its topic sentence.
- Keep noun clusters to three words or fewer.
- Use articles (a, an, the) and complete sentences; do not drop words to save space.
