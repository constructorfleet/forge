package linear

import "github.com/Teagan42/forge/internal/tracker"

// Capabilities reports the optional behaviors this Client supports.
//
// NativeDependencyLinks is always true: unlike GitHub/GitLab, where typed
// issue links are a probed, tier-gated feature with a body-block fallback,
// Linear's issue relations are a native, unconditional part of its data
// model, so there is no capability to probe (this ticket's "DependencyStore
// via native relations" decision).
//
// PlanningMirror is false: no planning-mirror projection behavior is built
// yet (see the doc comment on tracker.Capabilities).
func (c *Client) Capabilities() tracker.Capabilities {
	return tracker.Capabilities{
		PlanningMirror:        false,
		NativeDependencyLinks: true,
	}
}
