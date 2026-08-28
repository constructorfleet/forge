package config

// The raw* types mirror the public Config types but use pointers (or nil
// slices/maps) for every field that has a default. This lets Unmarshal tell
// "field absent" (nil) apart from "field explicitly set to the zero value"
// (non-nil pointer to zero), which is what lets partial configs receive
// defaults for only the sections/fields the caller omitted.
type rawConfig struct {
	Version      *int             `yaml:"version"`
	Tracker      *rawTracker      `yaml:"tracker"`
	Git          *rawGit          `yaml:"git"`
	Execution    *rawExecution    `yaml:"execution"`
	Retry        *rawRetry        `yaml:"retry"`
	Workflow     *rawWorkflow     `yaml:"workflow"`
	Quality      *rawQuality      `yaml:"quality"`
	PullRequests *rawPullRequests `yaml:"pull_requests"`
	CI           *rawCI           `yaml:"ci"`
	Blocked      *rawBlocked      `yaml:"blocked"`
	Agent        *rawAgent        `yaml:"agent"`
	Dependencies *rawDependencies `yaml:"dependencies"`
}

type rawTracker struct {
	Type *string `yaml:"type"`
}

type rawGit struct {
	Base           *string `yaml:"base"`
	BranchTemplate *string `yaml:"branch_template"`
	WorktreeRoot   *string `yaml:"worktree_root"`
}

type rawExecution struct {
	MaxParallel *int `yaml:"max_parallel"`
}

type rawRetry struct {
	Gate   *int `yaml:"gate"`
	Review *int `yaml:"review"`
	CI     *int `yaml:"ci"`
}

type rawWorkflow struct {
	Implementation *string `yaml:"implementation"`
	Review         *bool   `yaml:"review"`
}

type rawQuality struct {
	Gates []rawGate `yaml:"gates"`
}

type rawGate struct {
	Name    string `yaml:"name"`
	Command string `yaml:"command"`
}

type rawPullRequests struct {
	Enabled *bool `yaml:"enabled"`
	WatchCI *bool `yaml:"watch_ci"`
}

type rawCI struct {
	RequiredChecks *rawRequiredChecks `yaml:"required_checks"`
}

type rawRequiredChecks struct {
	Mode   *string  `yaml:"mode"`
	Checks []string `yaml:"checks"`
}

type rawBlocked struct {
	Label   *string `yaml:"label"`
	Comment *bool   `yaml:"comment"`
}

type rawAgent struct {
	Provider *string `yaml:"provider"`
}

type rawDependencies struct {
	Overrides map[string][]string `yaml:"overrides"`
}
