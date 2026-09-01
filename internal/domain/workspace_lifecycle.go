package domain

// WorkspaceLifecycle is the lifecycle state of a remote execution's
// Workspace. It is an execution-substrate concept, kept separate from
// IssueState (see state.go): only a worker that stops heartbeating past its
// ExecutionLease's expiry drives its Workspace to LOST (ADR 0020); a
// worker-reported Agent or Quality Gate failure is an ordinary IssueState
// failure and leaves the Workspace lifecycle at ACTIVE.
type WorkspaceLifecycle string

const (
	// WorkspaceLifecycleActive is a Workspace whose worker is still
	// reachable (or was never remote).
	WorkspaceLifecycleActive WorkspaceLifecycle = "ACTIVE"
	// WorkspaceLifecycleLost is a Workspace whose worker stopped
	// heartbeating past its lease's expiry, so the Workspace is no longer
	// authoritative.
	WorkspaceLifecycleLost WorkspaceLifecycle = "LOST"
)
