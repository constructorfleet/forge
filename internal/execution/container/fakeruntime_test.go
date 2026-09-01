package container

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/execution"
)

func startFakeRuntimeMountedAt(t *testing.T, hostDir string) (*FakeRuntime, ContainerHandle) {
	t.Helper()
	runtime := NewFakeRuntime()
	handle, err := runtime.Start(context.Background(), ContainerSpec{
		Image:  "forge/agent:latest",
		Mounts: []Mount{{HostPath: hostDir, ContainerPath: WorkspaceMountPath}},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return runtime, handle
}

func TestFakeRuntime_ExecRunsCommandInBindMountedHostDirectory(t *testing.T) {
	hostDir := t.TempDir()
	runtime, handle := startFakeRuntimeMountedAt(t, hostDir)

	result, err := runtime.Exec(context.Background(), handle, execution.Command{Name: "pwd", Command: "pwd"})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0 (stderr: %s)", result.ExitCode, result.Stderr)
	}
	if got := filepath.Clean(strings.TrimSpace(result.Stdout)); got != filepath.Clean(hostDir) {
		t.Errorf("Exec ran in %q, want %q (the bind-mounted host dir)", got, hostDir)
	}
}

func TestFakeRuntime_ExecHonorsWorkDirRelativeToMount(t *testing.T) {
	hostDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(hostDir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	wantDir := filepath.Join(hostDir, "sub")
	runtime, handle := startFakeRuntimeMountedAt(t, hostDir)

	result, err := runtime.Exec(context.Background(), handle, execution.Command{Name: "pwd", Command: "pwd", WorkDir: "sub"})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if got := filepath.Clean(strings.TrimSpace(result.Stdout)); got != filepath.Clean(wantDir) {
		t.Errorf("Exec ran in %q, want %q", got, wantDir)
	}
}

func TestFakeRuntime_ExecReportsNonZeroExitCode(t *testing.T) {
	runtime, handle := startFakeRuntimeMountedAt(t, t.TempDir())

	result, err := runtime.Exec(context.Background(), handle, execution.Command{Name: "fail", Command: "exit 3"})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if result.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", result.ExitCode)
	}
}

func TestFakeRuntime_ExecForwardsStdinAndEnv(t *testing.T) {
	runtime, handle := startFakeRuntimeMountedAt(t, t.TempDir())

	result, err := runtime.Exec(context.Background(), handle, execution.Command{
		Name:    "echo-stdin-and-env",
		Command: `cat; echo "VAR=$VAR"`,
		Stdin:   "from stdin",
		Env:     []string{"VAR=from-env"},
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if !strings.Contains(result.Stdout, "from stdin") {
		t.Errorf("Stdout = %q, want it to contain the forwarded stdin", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "VAR=from-env") {
		t.Errorf("Stdout = %q, want it to contain the forwarded env var", result.Stdout)
	}
}

func TestFakeRuntime_ExecReturnsErrorForUnknownHandle(t *testing.T) {
	runtime := NewFakeRuntime()

	if _, err := runtime.Exec(context.Background(), ContainerHandle("no-such-container"), execution.Command{Name: "noop", Command: "true"}); err == nil {
		t.Error("Exec() error = nil, want error for a handle Start never returned")
	}
}

func TestFakeRuntime_ExecRecordsEveryCall(t *testing.T) {
	runtime, handle := startFakeRuntimeMountedAt(t, t.TempDir())

	if _, err := runtime.Exec(context.Background(), handle, execution.Command{Name: "one", Command: "true"}); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if _, err := runtime.Exec(context.Background(), handle, execution.Command{Name: "two", Command: "true"}); err != nil {
		t.Fatalf("Exec: %v", err)
	}

	executed := runtime.Executed()
	if len(executed) != 2 {
		t.Fatalf("len(Executed()) = %d, want 2", len(executed))
	}
	if executed[0].Handle != handle || executed[0].Command.Name != "one" {
		t.Errorf("Executed()[0] = %+v, want Handle %q, Command.Name \"one\"", executed[0], handle)
	}
	if executed[1].Handle != handle || executed[1].Command.Name != "two" {
		t.Errorf("Executed()[1] = %+v, want Handle %q, Command.Name \"two\"", executed[1], handle)
	}
}
