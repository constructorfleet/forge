package gitlab

import "github.com/Teagan42/forge/internal/tracker"

// Capabilities reports the optional behaviors this Client supports.
//
// PlanningMirror is false: no planning-mirror projection behavior is built
// yet (see the doc comment on tracker.Capabilities).
//
// NativeDependencyLinks reports what the last issue-link probe found (see
// fetchBlockedBy). Capabilities makes no network request, so the value is
// false until the Client reads dependencies for the first time. A caller
// that wants an accurate answer must read one Issue first.
func (c *Client) Capabilities() tracker.Capabilities {
	c.mu.Lock()
	defer c.mu.Unlock()
	return tracker.Capabilities{
		PlanningMirror:        false,
		NativeDependencyLinks: c.linksProbed && c.linksAvailable,
	}
}
