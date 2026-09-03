package clicommon

import (
	"fmt"
	"strings"

	"github.com/Teagan42/forge/internal/agent"
)

// ProviderLimitReason is the fixed phrase every provider-limit detection
// stamps into the AgentResult.Summary it produces (see DetectProviderLimit).
// Callers can show it to operators without matching on free text.
const ProviderLimitReason = "provider limit reached"

// providerLimitPatterns are case-insensitive substrings that name a
// coding-agent provider's own rate-limit, quota, or usage-cap error, as
// opposed to a genuine Agent failure or code defect (issue #416). Sourced
// from the wording providers commonly use in CLI stderr/stdout when a call
// is rejected for exceeding a limit.
var providerLimitPatterns = []string{
	"rate limit",
	"rate_limit",
	"ratelimit",
	"usage limit",
	"usage cap",
	"quota exceeded",
	"resource_exhausted",
	"too many requests",
	"429 too many requests",
	"you've hit your usage limit",
	"you have hit your usage limit",
}

// DetectProviderLimit scans texts for a provider rate-limit, quota, or
// usage-cap error and reports the first match, quoting the offending line
// verbatim so the surfaced cause is the provider's own wording rather than a
// generic label. ok is false when none of texts names a provider limit.
func DetectProviderLimit(texts ...string) (ok bool, matchedLine string) {
	for _, text := range texts {
		if text == "" {
			continue
		}
		for _, line := range strings.Split(text, "\n") {
			lower := strings.ToLower(line)
			for _, pattern := range providerLimitPatterns {
				if strings.Contains(lower, pattern) {
					return true, strings.TrimSpace(line)
				}
			}
		}
	}
	return false, ""
}

// ProviderLimitSummary composes the Summary every adapter reports for a
// detected provider limit, so the message shape stays identical across
// backends instead of each call site repeating its own fmt.Sprintf (issue
// #416). backendName identifies the calling adapter (e.g. "claude",
// "codex"); line is the offending provider-output line DetectProviderLimit
// matched.
func ProviderLimitSummary(backendName, line string) string {
	return fmt.Sprintf("%s adapter: %s: %s", backendName, ProviderLimitReason, line)
}

// ProviderLimitResult returns the common AgentResult for a detected
// provider limit. All CLI adapters use this helper so the status and
// diagnostic shape stay identical.
func ProviderLimitResult(backendName, line, stdout, stderr string) agent.AgentResult {
	return agent.AgentResult{
		Status: agent.StatusProviderLimit,
		Summary: DiagnosticSummary(
			ProviderLimitSummary(backendName, line),
			stdout, stderr,
		),
	}
}
