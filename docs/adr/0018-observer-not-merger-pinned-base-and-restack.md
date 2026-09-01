# Forge observes merges and restacks; it does not merge pull requests

Forge does not perform pull-request merges. A human, or a separate policy
outside Forge, decides when a prerequisite Issue's pull request merges.
Forge only watches for that merge and repairs the stack it built on top of
the prerequisite. This ADR records that observer-not-merger stance, and the
pinned-base and restack model the stacked-branch maintenance workstream
(issue #288) builds on it.

## Pinned base, not a moving branch name

ADR 0012 stacks a dependent Issue's Worker on its Dependencies' resulting
branches instead of the Execution's original base branch. Until this ADR,
Forge captured that base as a branch name in the `worker.base_captured`
event. A branch name is not enough to repair a stack after the prerequisite
merges: a squash-merge removes the prerequisite's commits, so the branch no
longer names a commit the dependent's history can rebase against.

Forge now pins a stacked or integration base to the exact commit SHA the
dependent's Worker started from, not the branch name. This extends the
SHA-pinning that a no-Dependency Issue already used for its base (ADR
0006). The pinned SHA is the rebase boundary a later restack step uses: it
identifies precisely which commits are the prerequisite's own work, so a
restack can replay the dependent's commits after that boundary onto the
prerequisite's new state, without guessing which commits belong to whom.

## Restack model

When Forge observes that a prerequisite Issue's pull request has merged, it
repairs each dependent stacked on that prerequisite:

- Forge identifies the dependent's pinned old-base SHA (the exact commit
  the dependent's Worker started from).
- Forge rebases the dependent's branch from that pinned SHA onto the
  prerequisite's new merged state, replaying only the dependent's own
  commits.
- Forge repeats this for every dependent stacked on the merged prerequisite,
  so a chain of stacked Issues stays consistent after each merge.

Forge never merges a pull request itself, and it never guesses a rebase
boundary from a branch name that may have moved or disappeared. It acts
only on the commit SHA it pinned at Worker start.

This ADR is the foundation for the restack work in stacked-branch
maintenance workstream #288, tickets 2 through 4. This ticket (#330) adds
only the pinned-SHA datum; it changes no user-visible behavior yet.
