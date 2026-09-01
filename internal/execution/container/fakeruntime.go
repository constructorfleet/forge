package container

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Teagan42/forge/internal/execution"
)

// errUnknownHandle is Exec's outcome for a handle Start never returned (or
// one already removed). A real ContainerRuntime would report the same
// class of failure for an unknown/gone container.
var errUnknownHandle = errors.New("container: FakeRuntime: unknown container handle")

// execWaitDelay bounds how long Exec waits, after the command's own
// process exits, for the io-copy goroutines feeding stdout/stderr to see
// EOF. Mirrors internal/execution/localhost's waitDelay; see
// gate.ExecCommandRunner's doc comment (internal/gate/exec.go) for why
// this is needed.
const execWaitDelay = 2 * time.Second

// executedCall is one Exec invocation FakeRuntime recorded, for test
// assertions.
type executedCall struct {
	Handle  ContainerHandle
	Command execution.Command
}

// FakeRuntime is a deterministic ContainerRuntime for tests: it simulates
// start/stop/remove without a live container daemon, and it simulates Exec
// by actually running the command on the host, inside whichever host
// directory the started ContainerSpec bind-mounted at WorkspaceMountPath
// (or a subdirectory, per Command.WorkDir). Since a container backend's
// Workspace is a host bind mount (see internal/execution/container's
// package doc comment), running the real command against that host
// directory is a faithful simulation of "running inside the container"
// without needing a live container daemon, and lets tests observe real
// effects (e.g. a git commit landing in the host's object store).
type FakeRuntime struct {
	mu       sync.Mutex
	nextID   int
	started  []ContainerSpec
	specs    map[ContainerHandle]ContainerSpec
	stopped  []ContainerHandle
	removed  []ContainerHandle
	executed []executedCall
	startErr error
	exited   map[ContainerHandle]error
}

// NewFakeRuntime returns an empty FakeRuntime.
func NewFakeRuntime() *FakeRuntime {
	return &FakeRuntime{specs: make(map[ContainerHandle]ContainerSpec)}
}

// FailStart makes every later Start call fail with err instead of
// launching a container, simulating a runtime that rejects the launch
// (e.g. a missing image) for constructorfleet/forge#337.
func (r *FakeRuntime) FailStart(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.startErr = err
}

// Start records spec and returns a freshly minted handle for it, unless a
// prior FailStart call configured Start to fail instead.
func (r *FakeRuntime) Start(_ context.Context, spec ContainerSpec) (ContainerHandle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.startErr != nil {
		return "", r.startErr
	}
	r.nextID++
	handle := ContainerHandle(fmt.Sprintf("fake-container-%d", r.nextID))
	r.started = append(r.started, spec)
	r.specs[handle] = spec
	return handle, nil
}

// ExitUnexpectedly makes every later Exec call against handle fail with err
// instead of running the command, simulating a container that stops or
// crashes while a Worker step is in progress (constructorfleet/forge#338),
// rather than one that was never running at all (see errUnknownHandle).
func (r *FakeRuntime) ExitUnexpectedly(handle ContainerHandle, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.exited == nil {
		r.exited = make(map[ContainerHandle]error)
	}
	r.exited[handle] = err
}

// Exec runs cmd for real, on the host, inside the host directory handle's
// ContainerSpec bind-mounted at WorkspaceMountPath (joined with
// cmd.WorkDir, if set). It fails with errUnknownHandle if handle was never
// returned by Start (or has since been Removed), with the error a prior
// ExitUnexpectedly call configured if handle's container exited mid-run,
// and with an error if handle's ContainerSpec has no mount at
// WorkspaceMountPath.
func (r *FakeRuntime) Exec(ctx context.Context, handle ContainerHandle, cmd execution.Command) (execution.Result, error) {
	r.mu.Lock()
	spec, ok := r.specs[handle]
	exitErr := r.exited[handle]
	if ok {
		r.executed = append(r.executed, executedCall{Handle: handle, Command: cmd})
	}
	r.mu.Unlock()
	if exitErr != nil {
		return execution.Result{}, fmt.Errorf("container: exec in exited container: %w", exitErr)
	}
	if !ok {
		return execution.Result{}, fmt.Errorf("%w: %s", errUnknownHandle, handle)
	}

	hostDir, err := workspaceMountHostPath(spec)
	if err != nil {
		return execution.Result{}, err
	}
	dir := hostDir
	if cmd.WorkDir != "" {
		dir = filepath.Join(dir, cmd.WorkDir)
	}

	return runOnHost(ctx, dir, cmd)
}

// workspaceMountHostPath returns the HostPath of spec's Mount whose
// ContainerPath is WorkspaceMountPath.
func workspaceMountHostPath(spec ContainerSpec) (string, error) {
	for _, mount := range spec.Mounts {
		if mount.ContainerPath == WorkspaceMountPath {
			return mount.HostPath, nil
		}
	}
	return "", fmt.Errorf("container: FakeRuntime: no mount at %s", WorkspaceMountPath)
}

// runOnHost runs cmd.Command as a real subprocess via `sh -c`, rooted at
// dir, forwarding cmd.Stdin and cmd.Env (when set), and captures its
// stdout/stderr. Mirrors internal/execution/localhost's environment.Execute.
func runOnHost(ctx context.Context, dir string, cmd execution.Command) (execution.Result, error) {
	result := execution.Result{Name: cmd.Name, Command: cmd.Command, StartedAt: time.Now()}

	var execCmd *exec.Cmd
	if len(cmd.Args) > 0 {
		execCmd = exec.CommandContext(ctx, cmd.Args[0], cmd.Args[1:]...)
	} else {
		execCmd = exec.CommandContext(ctx, "sh", "-c", cmd.Command)
	}
	execCmd.Dir = dir
	execCmd.WaitDelay = execWaitDelay
	if cmd.Stdin != "" {
		execCmd.Stdin = strings.NewReader(cmd.Stdin)
	}
	if cmd.Env != nil {
		execCmd.Env = cmd.Env
	}
	var stdout, stderr bytes.Buffer
	execCmd.Stdout = &stdout
	execCmd.Stderr = &stderr

	runErr := execCmd.Run()
	result.FinishedAt = time.Now()
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()

	if runErr == nil {
		result.ExitCode = 0
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	result.ExitCode = -1
	return result, runErr
}

// Stop records handle as stopped.
func (r *FakeRuntime) Stop(_ context.Context, handle ContainerHandle) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped = append(r.stopped, handle)
	return nil
}

// Remove records handle as removed.
func (r *FakeRuntime) Remove(_ context.Context, handle ContainerHandle) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removed = append(r.removed, handle)
	return nil
}

// Started returns every ContainerSpec passed to Start so far, in call order.
func (r *FakeRuntime) Started() []ContainerSpec {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ContainerSpec, len(r.started))
	copy(out, r.started)
	return out
}

// Stopped returns every ContainerHandle passed to Stop so far, in call
// order.
func (r *FakeRuntime) Stopped() []ContainerHandle {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ContainerHandle, len(r.stopped))
	copy(out, r.stopped)
	return out
}

// Removed returns every ContainerHandle passed to Remove so far, in call
// order.
func (r *FakeRuntime) Removed() []ContainerHandle {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ContainerHandle, len(r.removed))
	copy(out, r.removed)
	return out
}

// Executed returns every Exec call FakeRuntime recorded so far (handle and
// Command), in call order, for tests to assert on — e.g. that no call
// carries a credential-shaped Env entry.
func (r *FakeRuntime) Executed() []executedCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]executedCall, len(r.executed))
	copy(out, r.executed)
	return out
}

var _ ContainerRuntime = (*FakeRuntime)(nil)
