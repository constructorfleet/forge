package domain

// IssueScope distinguishes an Issue that Forge executes (Managed) from an
// Issue referenced only as a Dependency and observed but never executed
// (External). See CONTEXT.md "External Issue".
type IssueScope string

const (
	// ScopeManaged is an Issue included in the Execution set and executed
	// by a Worker.
	ScopeManaged IssueScope = "MANAGED"
	// ScopeExternal is an Issue referenced as a Dependency but not included
	// in the Execution set. It is loaded into the DAG as an observed node,
	// tracked for satisfaction but never executed.
	ScopeExternal IssueScope = "EXTERNAL"
)
