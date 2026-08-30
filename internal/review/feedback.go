package review

import (
	"fmt"
	"strings"

	"github.com/Teagan42/forge/internal/agent"
)

// BuildFeedback turns Findings into bounded agent.Feedback routed back to
// the implementation Worker, mirroring gate.BuildFeedback. It includes
// every Finding passed in, not only blockers — advisory (WARNING/INFO)
// Findings are routed back too, since the Worker benefits from
// non-blocking signal just as much as from hard failures; callers decide
// which Findings warrant a fresh Review round, not BuildFeedback.
// agent.Feedback predates structured findings and has only a single Message
// string, so each Finding's axis/severity/location/message/remedy get
// folded into that string rather than dropped; one agent.Feedback is
// produced per Finding so a future retry loop (ticket 21) can still see
// them as discrete items.
func BuildFeedback(findings []Finding) []agent.Feedback {
	feedback := make([]agent.Feedback, 0, len(findings))
	for _, f := range findings {
		var b strings.Builder
		if f.Axis != "" {
			fmt.Fprintf(&b, "[%s] ", f.Axis)
		}
		b.WriteString(string(f.Severity))
		if f.File != "" {
			fmt.Fprintf(&b, " %s", f.File)
			if f.Line > 0 {
				fmt.Fprintf(&b, ":%d", f.Line)
			}
		}
		b.WriteString(": ")
		b.WriteString(f.Message)
		if f.Remedy != "" {
			fmt.Fprintf(&b, " (remedy: %s)", f.Remedy)
		}
		feedback = append(feedback, agent.Feedback{
			Source:  agent.FeedbackSourceReview,
			Message: b.String(),
		})
	}
	return feedback
}
