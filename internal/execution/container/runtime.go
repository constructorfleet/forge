// Package container implements the Container ExecutionBackend: it prepares
// a host git-worktree Workspace with the same machinery LocalHost uses
// (internal/workspace.Manager), then launches an isolated container with
// that Workspace bind-mounted, through a thin ContainerRuntime seam.
package container

import (
	"context"
	"errors"

	"github.com/Teagan42/forge/internal/execution"
)

// ErrRuntimeUnavailable is returned by a ContainerRuntime constructor when
// no container runtime (e.g. a docker or podman daemon) is available to
// drive. A caller selecting backend: container sees this at wiring time,
// before any container is launched.
var ErrRuntimeUnavailable = errors.New("container: no container runtime is available")

// WorkspaceMountPath is the fixed path inside a container where Prepare
// bind-mounts the host Workspace.
const WorkspaceMountPath = "/workspace"

// ContainerHandle identifies one container a ContainerRuntime started. It is
// opaque to the Container backend; only the ContainerRuntime implementation
// gives it meaning.
type ContainerHandle string

// Mount is one bind mount from a host path into a container path.
type Mount struct {
	HostPath      string
	ContainerPath string
}

// Resources gives a container coarse resource limits. Both fields are
// passed through to the container runtime unparsed (e.g. CPU "2", Memory
// "4Gi"); an empty field leaves that limit up to the runtime's own default.
type Resources struct {
	CPU    string
	Memory string
}

// ContainerSpec describes the container Start must launch: the image to
// run, the Resources to give it, and the Mounts to bind into it.
type ContainerSpec struct {
	Image  string
	CPU    string
	Memory string
	Mounts []Mount
}

// ContainerRuntime abstracts the container lifecycle the Container backend
// drives: start a container from a ContainerSpec, run a Command inside it,
// stop it, and remove it. One interface keeps this lifecycle fakeable, so
// tests can simulate it without a live container daemon.
type ContainerRuntime interface {
	// Start launches a container from spec and returns a handle for it.
	Start(ctx context.Context, spec ContainerSpec) (ContainerHandle, error)

	// Exec runs cmd inside the container handle identifies.
	Exec(ctx context.Context, handle ContainerHandle, cmd execution.Command) (execution.Result, error)

	// Stop stops the container handle identifies.
	Stop(ctx context.Context, handle ContainerHandle) error

	// Remove removes the container handle identifies. Callers stop the
	// container before they remove it.
	Remove(ctx context.Context, handle ContainerHandle) error
}
