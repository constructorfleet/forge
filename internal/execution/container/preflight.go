package container

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// candidateBinaries lists the CLI binaries DetectCLIRuntime probes, in
// probe order. Docker is tried first because it is the more common
// installation; Podman is the fallback.
var candidateBinaries = []string{"docker", "podman"}

// DetectCLIRuntime finds a usable CLIRuntime among candidateBinaries. A
// binary counts as available only when it actually answers `<binary>
// info` with a zero exit code — a live daemon check, not merely a PATH
// lookup — so an installed-but-unreachable daemon (e.g. Docker Desktop not
// running) is treated the same as a missing binary. It returns
// ErrRuntimeUnavailable, wrapped with every candidate's probe failure,
// when no candidate responds.
//
// Each candidate probes against its own sub-context, carved out of ctx's
// remaining deadline (when ctx has one) as an equal share of what remains
// for the candidates not yet tried. This keeps one slow-to-respond
// candidate (e.g. a hung, not-refused, docker socket) from consuming ctx's
// whole budget and starving the fallback probe of any time to run.
func DetectCLIRuntime(ctx context.Context, runner CommandRunner) (*CLIRuntime, error) {
	var probeErrs error
	remaining := len(candidateBinaries)
	for _, bin := range candidateBinaries {
		probeCtx, cancel := candidateContext(ctx, remaining)
		_, stderr, exitCode, err := runner.Run(probeCtx, []string{bin, "info"}, "")
		cancel()
		remaining--
		if err == nil && exitCode == 0 {
			return NewCLIRuntime(bin, runner), nil
		}
		if err == nil {
			err = fmt.Errorf("exited %d: %s", exitCode, strings.TrimSpace(stderr))
		}
		probeErrs = errors.Join(probeErrs, fmt.Errorf("%s: %w", bin, err))
	}
	return nil, fmt.Errorf("%w: %w", ErrRuntimeUnavailable, probeErrs)
}

// candidateContext derives a per-candidate sub-context from parent for one
// probe in DetectCLIRuntime's loop. When parent carries a deadline, the
// sub-context gets an equal share of parent's remaining time, divided by
// remainingCandidates (the candidates left to probe, including this one),
// so earlier candidates cannot exhaust the time later candidates need.
// When parent has no deadline, or no candidates remain, the sub-context
// simply inherits parent's cancellation.
func candidateContext(parent context.Context, remainingCandidates int) (context.Context, context.CancelFunc) {
	deadline, ok := parent.Deadline()
	if !ok || remainingCandidates <= 0 {
		return context.WithCancel(parent)
	}
	share := time.Until(deadline) / time.Duration(remainingCandidates)
	if share <= 0 {
		return context.WithDeadline(parent, deadline)
	}
	return context.WithTimeout(parent, share)
}
