package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestVersionOutput builds the forge binary with an embedded commit SHA and
// build time (the way a real release build would, per issue #321) and
// confirms `forge version` prints both, so a stale binary is diagnosable
// without reading source.
func TestVersionOutput(t *testing.T) {
	bin := buildBinaryWithVersion(t, "abc1234", "2026-09-01T00:00:00Z")

	out, err := exec.Command(bin, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("forge version exited with error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "abc1234") {
		t.Errorf("forge version output missing embedded commit, got: %s", out)
	}
	if !strings.Contains(string(out), "2026-09-01T00:00:00Z") {
		t.Errorf("forge version output missing embedded build time, got: %s", out)
	}
}

// TestVersionOutput_DefaultsWhenUnstamped confirms a binary built without
// ldflags (e.g. a plain `go build`) still runs `forge version` and reports
// its unstamped state plainly, rather than crashing or printing nothing.
func TestVersionOutput_DefaultsWhenUnstamped(t *testing.T) {
	bin := buildBinary(t)

	out, err := exec.Command(bin, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("forge version exited with error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "unknown") {
		t.Errorf("forge version output should report an unknown commit when unstamped, got: %s", out)
	}
}

// buildBinaryWithVersion builds the forge binary with commit/buildTime
// stamped in via -ldflags -X, the way a release build embeds them.
func buildBinaryWithVersion(t *testing.T, commit, buildTime string) string {
	t.Helper()
	bin := t.TempDir() + "/forge"
	ldflags := "-X main.buildCommit=" + commit + " -X main.buildTime=" + buildTime
	cmd := exec.Command("go", "build", "-ldflags", ldflags, "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build forge binary: %v\n%s", err, out)
	}
	return bin
}
