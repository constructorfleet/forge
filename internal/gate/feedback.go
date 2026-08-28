package gate

import (
	"fmt"
	"strings"

	"github.com/Teagan42/forge/internal/agent"
)

// BuildFeedback turns a failing gate Result into bounded agent.Feedback
// (CONTEXT.md "Gate Runner"), shaped per IDEATION.md §22's example: gate
// name, command, exit code, and the (already bounded, see boundedWriter)
// relevant output. Callers should only pass a Result with Passed == false.
func BuildFeedback(res Result) agent.Feedback {
	var b strings.Builder
	fmt.Fprintf(&b, "Quality gate failed:\nGate: %s\nCommand: %s\nExit code: %d\n", res.Name, res.Command, res.ExitCode)
	if res.Stdout != "" {
		fmt.Fprintf(&b, "Stdout:\n%s\n", res.Stdout)
	}
	if res.Stderr != "" {
		fmt.Fprintf(&b, "Stderr:\n%s\n", res.Stderr)
	}
	return agent.Feedback{Source: agent.FeedbackSourceGate, Message: b.String()}
}
