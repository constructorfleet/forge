package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestHelpOutput builds and runs the forge binary to confirm it prints help
// text with no arguments and with --help, per ticket 11's acceptance
// criteria ("CLI executable runs and prints help").
func TestHelpOutput(t *testing.T) {
	bin := buildBinary(t)

	for _, args := range [][]string{{}, {"--help"}} {
		out, err := exec.Command(bin, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("forge %v exited with error: %v\noutput: %s", args, err, out)
		}
		if !strings.Contains(string(out), "forge - deterministic orchestration") {
			t.Fatalf("forge %v did not print expected help text, got: %s", args, out)
		}
	}
}

func buildBinary(t *testing.T) string {
	t.Helper()
	bin := t.TempDir() + "/forge"
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build forge binary: %v\n%s", err, out)
	}
	return bin
}
