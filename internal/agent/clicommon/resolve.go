package clicommon

import (
	"fmt"
	"strings"

	"github.com/Teagan42/forge/internal/agent"
)

// Resolve turns a parsed StructuredResult (as returned by
// ParseStructuredResult) into an agent.AgentResult, applying the same
// status-handling rules internal/agent/claude's Adapter.Execute applies:
// a missing/unrecognized structured result, or a NEEDS_INFO result missing
// its question, both degrade to FAILED with bounded diagnostics. backendName
// identifies the calling adapter (e.g. "codex", "opencode") only for
// diagnostic messages.
func Resolve(backendName string, structured StructuredResult, ok bool, exitCode int, stdout, stderr string) agent.AgentResult {
	if !ok {
		// A missing structured result is often not a genuine Agent failure:
		// the provider CLI may have exited early on its own rate limit,
		// quota, or usage-cap error before ever emitting the result envelope
		// (issue #416). Detecting that here, before degrading to FAILED,
		// keeps the true cause visible instead of hiding it behind "no
		// structured result found".
		if limited, line := DetectProviderLimit(stdout, stderr); limited {
			return ProviderLimitResult(backendName, line, stdout, stderr)
		}
		return agent.AgentResult{
			Status: agent.StatusFailed,
			Summary: DiagnosticSummary(
				fmt.Sprintf("%s adapter: no structured result found in output (exit code %d)", backendName, exitCode),
				stdout, stderr,
			),
		}
	}

	switch agent.AgentStatus(structured.Status) {
	case agent.StatusImplemented:
		return agent.AgentResult{Status: agent.StatusImplemented, Summary: structured.Summary, FollowUps: ToFollowUps(structured.FollowUps), Usage: ToTokenUsage(structured.Usage)}
	case agent.StatusFailed:
		return agent.AgentResult{Status: agent.StatusFailed, Summary: structured.Summary, FollowUps: ToFollowUps(structured.FollowUps), Usage: ToTokenUsage(structured.Usage)}
	case agent.StatusNeedsInfo:
		if structured.NeedsInfo == nil || strings.TrimSpace(structured.NeedsInfo.Question) == "" {
			return agent.AgentResult{
				Status: agent.StatusFailed,
				Summary: DiagnosticSummary(
					fmt.Sprintf("%s adapter: NEEDS_INFO result missing a needs_info question", backendName),
					stdout, stderr,
				),
			}
		}
		return agent.AgentResult{
			Status:  agent.StatusNeedsInfo,
			Summary: structured.Summary,
			NeedsInfo: &agent.NeedsInfoDetail{
				Question: structured.NeedsInfo.Question,
				Context:  structured.NeedsInfo.Context,
			},
			FollowUps: ToFollowUps(structured.FollowUps),
			Usage:     ToTokenUsage(structured.Usage),
		}
	default:
		// ParseStructuredResult only returns ok=true for recognized
		// statuses, so this is unreachable in practice; handled for
		// exhaustiveness.
		return agent.AgentResult{
			Status:  agent.StatusFailed,
			Summary: DiagnosticSummary(fmt.Sprintf("%s adapter: unrecognized status %q", backendName, structured.Status), stdout, stderr),
		}
	}
}
