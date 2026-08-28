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
	dir := initGitFixture(t)

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

// TestInit_RefusesToOverwriteExistingConfigWithoutForce confirms forge init
// won't clobber a hand-edited .forge.yaml unless --force is passed.
func TestInit_RefusesToOverwriteExistingConfigWithoutForce(t *testing.T) {
	bin := buildBinary(t)
	dir := initGitFixture(t)

	yamlPath := filepath.Join(dir, ".forge.yaml")
	existing := "# hand-edited, do not clobber\nversion: 1\n"
	if err := os.WriteFile(yamlPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command(bin, "init", dir).CombinedOutput()
	if err == nil {
		t.Fatalf("forge init unexpectedly succeeded without --force, output: %s", out)
	}
	if !strings.Contains(string(out), "--force") {
		t.Errorf("error output should mention --force, got: %s", out)
	}

	data, readErr := os.ReadFile(yamlPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != existing {
		t.Errorf(".forge.yaml was overwritten without --force:\n%s", data)
	}

	// --force overwrites it.
	out, err = exec.Command(bin, "init", "--force", dir).CombinedOutput()
	if err != nil {
		t.Fatalf("forge init --force failed: %v\n%s", err, out)
	}
	data, readErr = os.ReadFile(yamlPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) == existing {
		t.Errorf(".forge.yaml was not overwritten with --force")
	}
}

// initGitFixture creates a minimal git+Go repo fixture and returns its
// directory.
func initGitFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/foo\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
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
