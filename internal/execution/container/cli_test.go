package container_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/execution"
	"github.com/Teagan42/forge/internal/execution/container"
)

// fakeCommandRunner is a deterministic container.CommandRunner for tests:
// it records every call it received and returns a scripted (stdout,
// stderr, exitCode, err) per call, so CLIRuntime tests never shell out to a
// real docker/podman binary.
type fakeCommandRunner struct {
	calls           [][]string
	stdins          []string
	ctxs            []context.Context
	results         []fakeResult
	next            int
	envFileContents []string
}

type fakeResult struct {
	stdout   string
	stderr   string
	exitCode int
	err      error
}

func (f *fakeCommandRunner) Run(ctx context.Context, args []string, stdin string) (string, string, int, error) {
	f.calls = append(f.calls, args)
	f.stdins = append(f.stdins, stdin)
	f.ctxs = append(f.ctxs, ctx)
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--env-file" {
			contents, err := os.ReadFile(args[i+1])
			if err != nil {
				contents = []byte("<read error: " + err.Error() + ">")
			}
			f.envFileContents = append(f.envFileContents, string(contents))
		}
	}
	if f.next >= len(f.results) {
		return "", "", 0, nil
	}
	r := f.results[f.next]
	f.next++
	return r.stdout, r.stderr, r.exitCode, r.err
}

func TestCLIRuntime_StartRunsCreateAndReturnsHandle(t *testing.T) {
	runner := &fakeCommandRunner{results: []fakeResult{{stdout: "abc123\n"}}}
	rt := container.NewCLIRuntime("docker", runner)

	handle, err := rt.Start(context.Background(), container.ContainerSpec{
		Image:  "forge/agent:latest",
		CPU:    "2",
		Memory: "4Gi",
		Mounts: []container.Mount{{HostPath: "/host/ws", ContainerPath: container.WorkspaceMountPath}},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if handle != "abc123" {
		t.Errorf("handle = %q, want %q", handle, "abc123")
	}

	if len(runner.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(runner.calls))
	}
	args := runner.calls[0]
	if args[0] != "docker" || args[1] != "run" {
		t.Errorf("args[0:2] = %v, want [docker run]", args[0:2])
	}
	joined := argsContain(args, "-v", "/host/ws:/workspace") &&
		argsContain(args, "--cpus", "2") &&
		argsContain(args, "--memory", "4g") &&
		argsContainOne(args, "forge/agent:latest")
	if !joined {
		t.Errorf("args = %v, missing expected mount/resource/image flags", args)
	}
}

func TestCLIRuntime_StartConvertsMemoryQuantityToDockerUnits(t *testing.T) {
	cases := []struct {
		memory string
		want   string
	}{
		{"512Mi", "512m"},
		{"1Ki", "1k"},
		{"2Gi", "2g"},
		{"1Ti", "1024g"},
		{"1024", "1024b"},
	}
	for _, c := range cases {
		runner := &fakeCommandRunner{results: []fakeResult{{stdout: "abc123\n"}}}
		rt := container.NewCLIRuntime("docker", runner)

		if _, err := rt.Start(context.Background(), container.ContainerSpec{
			Image:  "forge/agent:latest",
			Memory: c.memory,
		}); err != nil {
			t.Fatalf("Start(%q): %v", c.memory, err)
		}

		if !argsContain(runner.calls[0], "--memory", c.want) {
			t.Errorf("Start(%q): args = %v, want --memory %s", c.memory, runner.calls[0], c.want)
		}
	}
}

func TestCLIRuntime_StartRejectsInvalidMemoryQuantity(t *testing.T) {
	runner := &fakeCommandRunner{results: []fakeResult{{stdout: "abc123\n"}}}
	rt := container.NewCLIRuntime("docker", runner)

	_, err := rt.Start(context.Background(), container.ContainerSpec{
		Image:  "forge/agent:latest",
		Memory: "not-a-quantity",
	})
	if err == nil {
		t.Fatal("Start: want error for invalid memory quantity, got nil")
	}
}

func TestCLIRuntime_StartFailsOnNonZeroExit(t *testing.T) {
	runner := &fakeCommandRunner{results: []fakeResult{{stderr: "no such image", exitCode: 1}}}
	rt := container.NewCLIRuntime("docker", runner)

	_, err := rt.Start(context.Background(), container.ContainerSpec{Image: "missing:latest"})
	if err == nil {
		t.Fatal("Start: want error for non-zero exit, got nil")
	}
}

func TestCLIRuntime_StartFailsWhenBinaryCannotRun(t *testing.T) {
	wantErr := errors.New("boom")
	runner := &fakeCommandRunner{results: []fakeResult{{err: wantErr}}}
	rt := container.NewCLIRuntime("docker", runner)

	_, err := rt.Start(context.Background(), container.ContainerSpec{Image: "forge/agent:latest"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Start: err = %v, want wrapping %v", err, wantErr)
	}
}

func TestCLIRuntime_ExecRunsInsideWorkspaceMount(t *testing.T) {
	runner := &fakeCommandRunner{results: []fakeResult{{stdout: "hello\n", exitCode: 0}}}
	rt := container.NewCLIRuntime("docker", runner)

	result, err := rt.Exec(context.Background(), container.ContainerHandle("abc123"), execution.Command{
		Name:    "gate",
		Command: "echo hello",
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if result.Stdout != "hello\n" || result.ExitCode != 0 {
		t.Errorf("result = %+v, want stdout %q exitCode 0", result, "hello\n")
	}

	args := runner.calls[0]
	if args[0] != "docker" || args[1] != "exec" {
		t.Fatalf("args[0:2] = %v, want [docker exec]", args[0:2])
	}
	if !argsContain(args, "-w", container.WorkspaceMountPath) {
		t.Errorf("args = %v, want -w %s", args, container.WorkspaceMountPath)
	}
	if !argsContain(args, "abc123", "sh") {
		t.Errorf("args = %v, want handle followed by sh", args)
	}
}

func TestCLIRuntime_ExecJoinsWorkDirUnderWorkspaceMount(t *testing.T) {
	runner := &fakeCommandRunner{results: []fakeResult{{}}}
	rt := container.NewCLIRuntime("docker", runner)

	if _, err := rt.Exec(context.Background(), container.ContainerHandle("abc123"), execution.Command{
		Command: "go test ./...",
		WorkDir: "sub/dir",
	}); err != nil {
		t.Fatalf("Exec: %v", err)
	}

	args := runner.calls[0]
	if !argsContain(args, "-w", "/workspace/sub/dir") {
		t.Errorf("args = %v, want -w /workspace/sub/dir", args)
	}
}

func TestCLIRuntime_ExecRejectsWorkDirEscapingWorkspaceMount(t *testing.T) {
	cases := []string{"../", "../../etc", "..", "/etc"}
	for _, workDir := range cases {
		runner := &fakeCommandRunner{results: []fakeResult{{}}}
		rt := container.NewCLIRuntime("docker", runner)

		_, err := rt.Exec(context.Background(), container.ContainerHandle("abc123"), execution.Command{
			Command: "echo hi",
			WorkDir: workDir,
		})
		if workDir == "/etc" {
			// An absolute WorkDir joins as a relative component under the
			// mount (path.Join("/workspace", "/etc") == "/workspace/etc"),
			// so it stays confined and Exec must accept it.
			if err != nil {
				t.Errorf("Exec(WorkDir=%q): unexpected error %v", workDir, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("Exec(WorkDir=%q): want error for path escaping workspace mount, got nil", workDir)
		}
	}
}

func TestCLIRuntime_ExecUsesArgsDirectlyWhenSet(t *testing.T) {
	runner := &fakeCommandRunner{results: []fakeResult{{}}}
	rt := container.NewCLIRuntime("docker", runner)

	if _, err := rt.Exec(context.Background(), container.ContainerHandle("abc123"), execution.Command{
		Args: []string{"go", "vet", "./..."},
	}); err != nil {
		t.Fatalf("Exec: %v", err)
	}

	args := runner.calls[0]
	if !argsContain(args, "abc123", "go") || !argsContainOne(args, "vet") {
		t.Errorf("args = %v, want handle followed by go vet ./...", args)
	}
}

func TestCLIRuntime_ExecForwardsStdinAndSetsInteractiveFlag(t *testing.T) {
	runner := &fakeCommandRunner{results: []fakeResult{{}}}
	rt := container.NewCLIRuntime("docker", runner)

	if _, err := rt.Exec(context.Background(), container.ContainerHandle("abc123"), execution.Command{
		Command: "cat",
		Stdin:   "prompt text",
	}); err != nil {
		t.Fatalf("Exec: %v", err)
	}

	if runner.stdins[0] != "prompt text" {
		t.Errorf("stdin = %q, want %q", runner.stdins[0], "prompt text")
	}
	if !argsContainOne(runner.calls[0], "-i") {
		t.Errorf("args = %v, want -i for non-empty stdin", runner.calls[0])
	}
}

func TestCLIRuntime_ExecPassesEnvViaFileNotArgv(t *testing.T) {
	runner := &fakeCommandRunner{results: []fakeResult{{}}}
	rt := container.NewCLIRuntime("docker", runner)

	const secret = "sk-topsecret-value"
	if _, err := rt.Exec(context.Background(), container.ContainerHandle("abc123"), execution.Command{
		Command: "echo hi",
		Env:     []string{"FOO=bar", "ANTHROPIC_API_KEY=" + secret},
	}); err != nil {
		t.Fatalf("Exec: %v", err)
	}

	args := runner.calls[0]
	for _, a := range args {
		if strings.Contains(a, secret) {
			t.Fatalf("args = %v, secret value leaked onto argv", args)
		}
	}

	envFile := ""
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--env-file" {
			envFile = args[i+1]
		}
	}
	if envFile == "" {
		t.Fatalf("args = %v, want --env-file flag", args)
	}
	if len(runner.envFileContents) != 1 {
		t.Fatalf("envFileContents = %v, want exactly one env file read during Exec", runner.envFileContents)
	}
	contents := runner.envFileContents[0]
	if !strings.Contains(contents, "FOO=bar") || !strings.Contains(contents, "ANTHROPIC_API_KEY="+secret) {
		t.Errorf("env file contents = %q, want both entries", contents)
	}

	if _, err := os.Stat(envFile); !os.IsNotExist(err) {
		t.Errorf("env file %q still exists after Exec returned, want it removed", envFile)
	}
}

func TestCLIRuntime_ExecReplacesRatherThanAddsToContainerEnv(t *testing.T) {
	runner := &fakeCommandRunner{results: []fakeResult{{}}}
	rt := container.NewCLIRuntime("docker", runner)

	if _, err := rt.Exec(context.Background(), container.ContainerHandle("abc123"), execution.Command{
		Command: "echo hi",
		Env:     []string{"FOO=bar"},
	}); err != nil {
		t.Fatalf("Exec: %v", err)
	}

	args := runner.calls[0]
	script := ""
	for _, a := range args {
		if strings.Contains(a, "env -i") {
			script = a
		}
	}
	if script == "" {
		t.Fatalf("args = %v, want a script containing an `env -i` wrapper to clear the container's ambient env", args)
	}
	if !strings.Contains(script, `FOO="$FOO"`) {
		t.Errorf("script = %q, want env -i to re-set FOO from the shell's own (env-file-populated) environment", script)
	}
}

func TestCLIRuntime_ExecRejectsInvalidEnvVarName(t *testing.T) {
	runner := &fakeCommandRunner{results: []fakeResult{{}}}
	rt := container.NewCLIRuntime("docker", runner)

	_, err := rt.Exec(context.Background(), container.ContainerHandle("abc123"), execution.Command{
		Command: "echo hi",
		Env:     []string{"1INVALID=bar"},
	})
	if err == nil {
		t.Fatal("Exec: want error for invalid env var name, got nil")
	}
}

func TestCLIRuntime_StopAndRemove(t *testing.T) {
	runner := &fakeCommandRunner{results: []fakeResult{{}, {}}}
	rt := container.NewCLIRuntime("docker", runner)

	if err := rt.Stop(context.Background(), "abc123"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := rt.Remove(context.Background(), "abc123"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if got := runner.calls[0]; got[0] != "docker" || got[1] != "stop" || got[2] != "abc123" {
		t.Errorf("stop args = %v, want [docker stop abc123]", got)
	}
	if got := runner.calls[1]; got[0] != "docker" || got[1] != "rm" || got[2] != "abc123" {
		t.Errorf("remove args = %v, want [docker rm abc123]", got)
	}
}

func argsContain(args []string, flag, value string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func argsContainOne(args []string, value string) bool {
	for _, a := range args {
		if a == value {
			return true
		}
	}
	return false
}
