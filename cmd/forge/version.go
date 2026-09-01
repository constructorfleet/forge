package main

import (
	"fmt"
	"os"
)

// buildCommit and buildTime are stamped in at build time via
// `-ldflags "-X main.buildCommit=<sha> -X main.buildTime=<time>"` (see
// Makefile's `build` target). A binary built without ldflags — a plain
// `go build`/`go test` — keeps these defaults, so `forge version` still
// prints a plain, honest "unknown" instead of a misleading empty string.
var (
	buildCommit = "unknown"
	buildTime   = "unknown"
)

const versionUsage = `Usage: forge version

Print the build's embedded commit SHA and build time. Use this to check
that the installed binary matches the source, before you rely on it in
the dogfood loop (see docs/agents/handling-issues.md).
`

// runVersion implements `forge version`. Issue #321: the dogfood loop edits
// forge's source and runs the installed binary; nothing used to guarantee
// they matched, so a stale binary silently kept old behavior. This command
// makes the running binary's provenance diagnosable in seconds.
func runVersion(args []string) int {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprint(os.Stdout, versionUsage)
		return 0
	}
	fmt.Fprintf(os.Stdout, "forge commit %s, built %s\n", buildCommit, buildTime)
	return 0
}
