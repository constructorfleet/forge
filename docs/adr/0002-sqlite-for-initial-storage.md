# SQLite for initial storage

Execution state needs transactional persistence, restart recovery, and queryable history, but not multi-node access. SQLite provides all of this with no external service dependency — the orchestrator remains a single binary. Storage is abstracted behind an interface so Postgres can be added later when multi-node or higher concurrency demands it.
