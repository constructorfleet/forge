package main

import (
	"os"
	"os/exec"
	"path/filepath"
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

// TestInit_WritesLoadableForgeYAML runs `forge init` against a minimal Go
// repo fixture and confirms it writes a .forge.yaml that another `forge`
// invocation-worthy loader (config.Load, exercised directly in
// internal/initdiscovery) would accept, and that command output surfaces
// discovery notes.
func TestInit_WritesLoadableForgeYAML(t *testing.T) {
	bin := buildBinary(t)

	dir := t.TempDir()
	run := func(args ...string) (string, error) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if _, err := run("init", "-q", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if _, err := run("config", "user.email", "test@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := run("config", "user.name", "Test"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/foo\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command(bin, "init", dir).CombinedOutput()
	if err != nil {
		t.Fatalf("forge init exited with error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "wrote "+dir+"/.forge.yaml") {
		t.Errorf("forge init output missing confirmation, got: %s", out)
	}
	if !strings.Contains(string(out), "note:") {
		t.Errorf("forge init output missing discovery notes, got: %s", out)
	}

	yamlPath := filepath.Join(dir, ".forge.yaml")
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("read generated .forge.yaml: %v", err)
	}
	if !strings.Contains(string(data), "go build ./...") {
		t.Errorf(".forge.yaml missing detected go build command:\n%s", data)
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
