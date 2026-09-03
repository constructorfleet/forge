# Issue tracker: GitHub issues

Issues for this repo live as GitHub issues on `constructorfleet/forge`. Use the `gh` CLI
for all reads and writes. The `.scratch/` directory holds in-progress specs and
wayfinding maps only. It is not the issue backlog.

## Conventions

- One GitHub issue per implementation ticket. Reference a ticket by its issue number
  (`#123`).
- Triage state is a GitHub label. See `triage-labels.md` for the role strings.
- Comments are GitHub issue comments.
- Dependencies go in a `## Dependencies` block in the issue body, with one `- #123` line
  per prerequisite. This block is the canonical Dependency Source. Native GitHub
  "blocked by" relationships take precedence when they are set. See
  `docs/adr/0003-dependencies-in-issue-body.md`.
- A feature spec that is still in progress stays at `.scratch/<feature-slug>/spec.md`.
  Link to it from the issues it produces.

## When a skill says "publish to the issue tracker"

Create a GitHub issue:

```
gh issue create --repo constructorfleet/forge \
  --title "<title>" --body-file <path> --label needs-triage
```

Add a `## Dependencies` block to the body for each prerequisite issue.

## When a skill says "fetch the relevant ticket"

Read the issue with `gh`. The user normally passes the issue number directly.

```
gh issue view <number> --repo constructorfleet/forge --json number,title,body,labels,comments
```

## Common operations

| Operation      | Command                                                            |
| -------------- | ------------------------------------------------------------------ |
| List ready     | `gh issue list --label ready-for-agent`                            |
| Retriage       | `gh issue edit <n> --add-label <role> --remove-label needs-triage`  |
| Comment        | `gh issue comment <n> --body "<text>"`                             |
| Close          | `gh issue close <n> --reason completed`                            |
| Implement      | `forge execute <n>` (see `handling-issues.md`)                     |

Add `--repo constructorfleet/forge` when you do not run the command in a clone of the
repo.

## Wayfinding operations

Used by `/wayfinder`. Wayfinding stays local in `.scratch/`, because a map is
exploration state and not backlog. The **map** is a file with one **child** file per
ticket.

- **Map**: `.scratch/<effort>/map.md` — the Notes / Decisions-so-far / Fog body.
- **Child ticket**: `.scratch/<effort>/issues/NN-<slug>.md`, numbered from `01`, with the question in the body. A `Type:` line records the ticket type (`research`/`prototype`/`grilling`/`task`); a `Status:` line records `claimed`/`resolved`.
- **Blocking**: a `Blocked by: NN, NN` line near the top. A ticket is unblocked when every file it lists is `resolved`.
- **Frontier**: scan `.scratch/<effort>/issues/` for files that are open, unblocked, and unclaimed; first by number wins.
- **Claim**: set `Status: claimed` and save before any work.
- **Resolve**: append the answer under an `## Answer` heading, set `Status: resolved`, then append a context pointer (gist + link) to the map's Decisions-so-far in `map.md`.

When a wayfinding ticket turns into implementation work, file it as a GitHub issue and
link the map file from the issue body.
