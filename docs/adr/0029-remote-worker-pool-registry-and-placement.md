# The Remote worker pool selects workers by capability and capacity

Forge supports a Remote worker pool as an opt-in extension of the Remote backend.
The controller owns the registry. A worker joins the registry with a worker ID,
capabilities, load, and an authentication token. The controller rejects a worker
that cannot prove it can join the pool.

The registry stores execution-substrate state only. It records whether a worker
is available, busy, draining, or offline. It does not add Issue states. A missed
heartbeat makes a worker offline for placement. A drained worker can finish its
current work, but it receives no new work.

Placement is a pure function. It reads a registry snapshot and execution
requirements. It filters workers by state, backend support, container support,
labels, and free capacity. It then chooses the least-loaded worker. If two
workers have the same load, it chooses by worker ID. This keeps placement
deterministic and testable. It also keeps the decision away from the Agent.

The pool does not create a second execution path. After placement, the selected
worker is driven through the existing WorkerClient seam. The worker prepares the
Workspace, runs commands and the Agent, heartbeats the lease, returns a bundle,
and cleans up exactly as the single-worker Remote backend does.

The configuration is explicit. `execution.worker.endpoint` still selects the
single-worker Remote backend. `execution.worker.pool.enabled` selects the pool.
Pool authentication uses `auth_token_env`, not a literal secret in `.forge.yaml`.
The configuration can bootstrap known worker endpoints. The registry API is the
controller-side surface for dynamic worker joins.
