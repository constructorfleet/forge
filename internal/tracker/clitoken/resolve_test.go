package clitoken

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestResolveEnvironmentWinsWithoutRunningCLI(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "env-token")
	old := commandContext
	commandContext = func(context.Context, string, ...string) *exec.Cmd { t.Fatal("CLI must not run"); return nil }
	t.Cleanup(func() { commandContext = old })

	token, source := Resolve(context.Background(), "github", "GITHUB_TOKEN", "https://github.com")
	if token != "env-token" || source != SourceEnv {
		t.Fatalf("Resolve() = %q, %q", token, source)
	}
}

func fakeCommand(_ string, output string) func(context.Context, string, ...string) *exec.Cmd {
	return func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "echo", "-n", output)
	}
}

func writeTeaConfig(dir, config string) error {
	if err := os.MkdirAll(filepath.Join(dir, "tea"), 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "tea", "config.yml"), []byte(config), 0o600)
}

func TestResolveGitHubCLI(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	old := commandContext
	commandContext = fakeCommand("gh", "cli-token")
	t.Cleanup(func() { commandContext = old })

	token, source := Resolve(context.Background(), "github", "GITHUB_TOKEN", "https://ghe.example/api/v3")
	if token != "cli-token" || source != SourceCLI {
		t.Fatalf("Resolve() = %q, %q", token, source)
	}
}

func TestResolveNoneWhenCLIUnavailable(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	old := commandContext
	commandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "false")
	}
	t.Cleanup(func() { commandContext = old })

	token, source := Resolve(context.Background(), "github", "GITHUB_TOKEN", "https://github.com")
	if token != "" || source != SourceNone {
		t.Fatalf("Resolve() = %q, %q", token, source)
	}
}

func TestResolveGiteaConfigSelectsURL(t *testing.T) {
	t.Setenv("GITEA_TOKEN", "")
	oldDir, oldRun := userConfigDir, commandContext
	dir := t.TempDir()
	userConfigDir = func() (string, error) { return dir, nil }
	commandContext = func(context.Context, string, ...string) *exec.Cmd { return exec.Command("false") }
	t.Cleanup(func() { userConfigDir, commandContext = oldDir, oldRun })
	if err := writeTeaConfig(dir, "logins:\n  - name: one\n    url: https://gitea.example\n    token: tea-token\n    default: false\n"); err != nil {
		t.Fatal(err)
	}
	token, source := Resolve(context.Background(), "gitea", "GITEA_TOKEN", "https://gitea.example/api/v1")
	if token != "tea-token" || source != SourceCLI {
		t.Fatalf("Resolve() = %q, %q", token, source)
	}
}

func TestResolveGiteaMissingConfig(t *testing.T) {
	t.Setenv("GITEA_TOKEN", "")
	oldDir := userConfigDir
	userConfigDir = func() (string, error) { return "", errors.New("no config") }
	t.Cleanup(func() { userConfigDir = oldDir })
	if token, source := Resolve(context.Background(), "gitea", "GITEA_TOKEN", "https://gitea.example"); token != "" || source != SourceNone {
		t.Fatalf("Resolve() = %q, %q", token, source)
	}
}
