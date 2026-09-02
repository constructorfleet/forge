package container

import (
	"context"
	"fmt"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Teagan42/forge/internal/execution"
)

// keepAliveCommand and keepAliveArg are the entrypoint override Start gives
// a launched container, so it stays running (with no dependency on the
// image's own entrypoint or a shell) until Stop ends it. `sleep infinity`
// is available in essentially every Linux image forge/agent-style images
// build from.
const (
	keepAliveCommand = "sleep"
	keepAliveArg     = "infinity"
)

// CLIRuntime is a ContainerRuntime backed by a docker- or podman-compatible
// CLI binary. It drives the binary entirely through the injected
// CommandRunner seam, so tests can assert on the exact invocations without
// a live container daemon.
type CLIRuntime struct {
	binary string
	runner CommandRunner
}

// NewCLIRuntime returns a CLIRuntime that drives binary (e.g. "docker" or
// "podman") through runner.
func NewCLIRuntime(binary string, runner CommandRunner) *CLIRuntime {
	return &CLIRuntime{binary: binary, runner: runner}
}

var _ ContainerRuntime = (*CLIRuntime)(nil)

// Start launches a detached container from spec via `<binary> run -d`,
// with spec's Mounts bind-mounted and CPU/Memory limits applied when set,
// and an entrypoint override that keeps the container running (see
// keepAliveCommand) so later Exec calls have a live target. It returns the
// container ID `run` printed on stdout as the ContainerHandle.
func (r *CLIRuntime) Start(ctx context.Context, spec ContainerSpec) (ContainerHandle, error) {
	args := []string{r.binary, "run", "-d"}
	if spec.CPU != "" {
		args = append(args, "--cpus", spec.CPU)
	}
	if spec.Memory != "" {
		mem, err := dockerMemoryLimit(spec.Memory)
		if err != nil {
			return "", fmt.Errorf("container: start %s container: %w", r.binary, err)
		}
		args = append(args, "--memory", mem)
	}
	for _, m := range spec.Mounts {
		args = append(args, "-v", m.HostPath+":"+m.ContainerPath)
	}
	args = append(args, "--entrypoint", keepAliveCommand, spec.Image, keepAliveArg)

	stdout, stderr, exitCode, err := r.runner.Run(ctx, args, "")
	if err != nil {
		return "", fmt.Errorf("container: start %s container: %w", r.binary, err)
	}
	if exitCode != 0 {
		return "", fmt.Errorf("container: %s run exited %d: %s", r.binary, exitCode, strings.TrimSpace(stderr))
	}
	id := strings.TrimSpace(stdout)
	if id == "" {
		return "", fmt.Errorf("container: %s run: empty container id on stdout", r.binary)
	}
	return ContainerHandle(id), nil
}

// memoryQuantityPattern splits a Kubernetes-style memory quantity (e.g.
// "4Gi", "512Mi", "1024") into its numeric value and unit suffix, for
// dockerMemoryLimit.
var memoryQuantityPattern = regexp.MustCompile(`^(\d+)([A-Za-z]*)$`)

// dockerMemoryLimit converts mem, a Kubernetes-style memory quantity as
// documented on Resources.Memory (e.g. "4Gi", "512Mi"), to the single-letter
// unit docker's and podman's `--memory` flag accepts (b, k, m, or g). An
// empty mem returns an empty string unchanged. Ti/T values scale into g,
// since docker and podman accept no larger unit.
func dockerMemoryLimit(mem string) (string, error) {
	if mem == "" {
		return "", nil
	}
	m := memoryQuantityPattern.FindStringSubmatch(mem)
	if m == nil {
		return "", fmt.Errorf("invalid memory quantity %q", mem)
	}
	value, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid memory quantity %q: %w", mem, err)
	}
	switch m[2] {
	case "", "b", "B":
		return fmt.Sprintf("%db", value), nil
	case "k", "K", "Ki":
		return fmt.Sprintf("%dk", value), nil
	case "m", "M", "Mi":
		return fmt.Sprintf("%dm", value), nil
	case "g", "G", "Gi":
		return fmt.Sprintf("%dg", value), nil
	case "t", "T", "Ti":
		return fmt.Sprintf("%dg", value*1024), nil
	default:
		return "", fmt.Errorf("unsupported memory unit in %q", mem)
	}
}

// envVarNamePattern matches a POSIX-portable shell environment variable
// name, per IEEE Std 1003.1 ("Name" in XBD 3.235): a leading letter or
// underscore, then letters, digits, or underscores. Exec rejects any
// cmd.Env entry whose name does not match, since names that do pass are
// interpolated directly into a generated shell script (see Exec).
var envVarNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Exec runs cmd inside the container handle identifies, via
// `<binary> exec`. It runs in cmd.WorkDir joined under WorkspaceMountPath
// (or WorkspaceMountPath itself, when WorkDir is empty), through a POSIX
// join since the container's filesystem is Linux regardless of the host
// OS. Exec rejects a WorkDir that resolves outside WorkspaceMountPath (e.g.
// "../" or an absolute path), rather than pass it to `-w` unconfined. When
// cmd.Args is set it runs that argv directly, with no shell interpretation;
// otherwise it runs cmd.Command via `sh -c`.
//
// cmd.Env, when non-nil, fully replaces the executed process's
// environment, per the Command.Env contract (internal/execution.Command):
// none of the container's own ambient environment is visible to it. Exec
// achieves this without ever placing a value from cmd.Env on the
// docker/podman client's argv (where it would be visible to any other
// local user via `ps`/`/proc/<pid>/cmdline` on the host): the KEY=VALUE
// pairs go into a short-lived, 0600-permission file passed via
// `--env-file` (removed once Exec returns), and the executed command is
// wrapped in a small shell script that reconstructs `env -i KEY="$KEY"
// ...` from the (secret-free) variable *names* alone, relying on the
// shell to substitute each value from its own env-file-populated
// environment at run time.
func (r *CLIRuntime) Exec(ctx context.Context, handle ContainerHandle, cmd execution.Command) (execution.Result, error) {
	result := execution.Result{Name: cmd.Name, Command: cmd.Command, StartedAt: time.Now()}

	workDir := WorkspaceMountPath
	if cmd.WorkDir != "" {
		workDir = path.Join(WorkspaceMountPath, cmd.WorkDir)
		if workDir != WorkspaceMountPath && !strings.HasPrefix(workDir, WorkspaceMountPath+"/") {
			return execution.Result{}, fmt.Errorf("container: exec in %s: WorkDir %q escapes %s", handle, cmd.WorkDir, WorkspaceMountPath)
		}
	}

	args := []string{r.binary, "exec"}
	if cmd.Stdin != "" {
		args = append(args, "-i")
	}
	args = append(args, "-w", workDir)

	var envNames []string
	if len(cmd.Env) > 0 {
		var err error
		envNames, err = envVarNames(cmd.Env)
		if err != nil {
			return execution.Result{}, fmt.Errorf("container: exec in %s: %w", handle, err)
		}
		envFile, err := writeEnvFile(cmd.Env)
		if err != nil {
			return execution.Result{}, fmt.Errorf("container: exec in %s: %w", handle, err)
		}
		defer os.Remove(envFile)
		args = append(args, "--env-file", envFile)
	}

	args = append(args, string(handle))

	realArgv := cmd.Args
	if len(realArgv) == 0 {
		realArgv = []string{"sh", "-c", cmd.Command}
	}
	if len(envNames) > 0 {
		args = append(args, "sh", "-c", envReplaceScript(envNames), "--")
	}
	args = append(args, realArgv...)

	stdout, stderr, exitCode, err := r.runner.Run(ctx, args, cmd.Stdin)
	result.FinishedAt = time.Now()
	if err != nil {
		return execution.Result{}, fmt.Errorf("container: exec in %s: %w", handle, err)
	}
	result.Stdout = stdout
	result.Stderr = stderr
	result.ExitCode = exitCode
	return result, nil
}

// envVarNames extracts and validates the variable name from each "KEY=VALUE"
// entry in env, rejecting any entry with no '=' or a name that does not
// match envVarNamePattern, since valid names are interpolated directly into
// a generated shell script (see envReplaceScript).
func envVarNames(env []string) ([]string, error) {
	names := make([]string, 0, len(env))
	for _, e := range env {
		key, _, ok := strings.Cut(e, "=")
		if !ok || !envVarNamePattern.MatchString(key) {
			return nil, fmt.Errorf("invalid environment variable name in %q", e)
		}
		names = append(names, key)
	}
	return names, nil
}

// writeEnvFile writes env's "KEY=VALUE" entries, one per line, to a new
// temporary file for use as a `docker`/`podman exec --env-file` argument,
// and returns its path. The file is created with permissions restricted to
// the current user (Go's os.CreateTemp default, 0600), and the caller
// removes it once it is no longer needed.
func writeEnvFile(env []string) (string, error) {
	f, err := os.CreateTemp("", "forge-container-env-*")
	if err != nil {
		return "", fmt.Errorf("create env file: %w", err)
	}
	defer f.Close()
	for _, e := range env {
		if _, err := f.WriteString(e + "\n"); err != nil {
			_ = os.Remove(f.Name())
			return "", fmt.Errorf("write env file: %w", err)
		}
	}
	return f.Name(), nil
}

// envReplaceScript returns a `sh -c` script that clears the executed
// process's inherited environment and re-sets exactly the variables named
// in names, then execs its own arguments ("$@", populated by the "--"
// Exec appends after this script). Each name is interpolated literally
// into the script; envVarNames guarantees every name is a bare shell
// identifier, so this is safe. Each value comes from the shell's own
// environment (KEY="$KEY"), populated by the --env-file Exec passes,
// never from a literal embedded in the script.
func envReplaceScript(names []string) string {
	var b strings.Builder
	b.WriteString("exec env -i")
	for _, n := range names {
		fmt.Fprintf(&b, ` %s="$%s"`, n, n)
	}
	b.WriteString(` "$@"`)
	return b.String()
}

// Stop stops the container handle identifies via `<binary> stop`.
func (r *CLIRuntime) Stop(ctx context.Context, handle ContainerHandle) error {
	_, stderr, exitCode, err := r.runner.Run(ctx, []string{r.binary, "stop", string(handle)}, "")
	if err != nil {
		return fmt.Errorf("container: stop %s: %w", handle, err)
	}
	if exitCode != 0 {
		return fmt.Errorf("container: %s stop %s exited %d: %s", r.binary, handle, exitCode, strings.TrimSpace(stderr))
	}
	return nil
}

// Remove removes the container handle identifies via `<binary> rm`.
// Callers stop the container before they remove it.
func (r *CLIRuntime) Remove(ctx context.Context, handle ContainerHandle) error {
	_, stderr, exitCode, err := r.runner.Run(ctx, []string{r.binary, "rm", string(handle)}, "")
	if err != nil {
		return fmt.Errorf("container: remove %s: %w", handle, err)
	}
	if exitCode != 0 {
		return fmt.Errorf("container: %s rm %s exited %d: %s", r.binary, handle, exitCode, strings.TrimSpace(stderr))
	}
	return nil
}
