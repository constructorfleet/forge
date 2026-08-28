# Go for the orchestrator

The orchestrator is infrastructure — process execution, filesystem management, long-running daemons, state-machine orchestration — not an ML application. Go fits this profile: straightforward concurrency, strong process/filesystem primitives, easy static distribution, and no runtime dependency on the repository's own language environment. Python is capable but would risk entangling the orchestrator with the Python toolchain of the projects it orchestrates.
