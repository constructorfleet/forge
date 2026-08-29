package clicommon

import "strings"

// MaxDiagnosticLen bounds how much of stdout/stderr is folded into an
// AgentResult.Summary for FAILED outcomes, keeping diagnostics readable and
// bounded (see CONTEXT.md "Gate Runner" for the same bounded-feedback
// principle applied here to Agent diagnostics).
const MaxDiagnosticLen = 4000

// DiagnosticSummary composes a human-readable Summary for FAILED outcomes,
// folding in bounded captures of stdout/stderr so a human (or a repair
// prompt on retry, via Feedback) has enough to diagnose the failure.
func DiagnosticSummary(prefix, stdout, stderr string) string {
	var b strings.Builder
	b.WriteString(prefix)
	if stdout = strings.TrimSpace(stdout); stdout != "" {
		b.WriteString("\n\nstdout:\n")
		b.WriteString(Truncate(stdout, MaxDiagnosticLen))
	}
	if stderr = strings.TrimSpace(stderr); stderr != "" {
		b.WriteString("\n\nstderr:\n")
		b.WriteString(Truncate(stderr, MaxDiagnosticLen))
	}
	return b.String()
}

// Truncate bounds s to at most n bytes, marking that it was cut so readers
// know the diagnostic is partial.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n... (truncated)"
}
