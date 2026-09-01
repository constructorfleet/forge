package remote

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/Teagan42/forge/internal/execution"
)

// BundlePublisher implements engine.Publisher's Commit/Push shape for the
// Remote backend: Commit imports the worker's finished Git bundle into the
// controller's canonical repository instead of committing in place there,
// and Push publishes that canonical repository's branch exactly like every
// other backend's Publisher. No shared filesystem between controller and
// worker is required (constructorfleet/forge#340).
type BundlePublisher struct {
	// RepoPath is the canonical repository's root, controller-side. Commit
	// imports a worker's bundle here; Push publishes from here.
	RepoPath string
}

// NewBundlePublisher returns a BundlePublisher that imports bundles into,
// and publishes from, the canonical repository at repoPath.
func NewBundlePublisher(repoPath string) *BundlePublisher {
	return &BundlePublisher{RepoPath: repoPath}
}

// Commit fetches env's worker's finished Git bundle (WorkerClient.
// FetchResult) and imports it into the canonical repository as
// env.Workspace().Branch, at the bundle's HeadSHA. message is unused: the
// worker's bundle already carries its own commit(s); Commit only needs to
// land them on the controller side. Returns the imported HeadSHA.
func (p *BundlePublisher) Commit(ctx context.Context, env execution.ExecutionEnvironment, _ string) (string, error) {
	e, ok := env.(*environment)
	if !ok {
		return "", fmt.Errorf("remote: BundlePublisher requires a Remote environment, got %T", env)
	}

	result, err := e.worker.FetchResult(ctx, e.handle)
	if err != nil {
		return "", fmt.Errorf("remote: fetch worker result: %w", err)
	}
	if len(result.Bundle) == 0 {
		return "", errors.New("remote: worker returned an empty bundle")
	}

	if err := p.importBundle(ctx, result.Bundle, e.workspace.Branch); err != nil {
		return "", err
	}
	return result.HeadSHA, nil
}

// importBundle writes bundle to a temporary file, verifies it against the
// canonical repository, then fetches its HEAD into refs/heads/branch there
// (creating or fast-forwarding it) — the standard clone-in/bundle-out
// import sequence, and the only step that touches the canonical repository
// before Push.
func (p *BundlePublisher) importBundle(ctx context.Context, bundle []byte, branch string) error {
	dir, err := os.MkdirTemp("", "forge-remote-bundle-*")
	if err != nil {
		return fmt.Errorf("remote: create temp dir for bundle import: %w", err)
	}
	defer os.RemoveAll(dir)

	bundlePath := filepath.Join(dir, "worker.bundle")
	if err := os.WriteFile(bundlePath, bundle, 0o600); err != nil {
		return fmt.Errorf("remote: write bundle: %w", err)
	}

	if out, err := exec.CommandContext(ctx, "git", "-C", p.RepoPath, "bundle", "verify", bundlePath).CombinedOutput(); err != nil {
		return fmt.Errorf("remote: verify bundle: %w: %s", err, out)
	}

	refspec := "HEAD:refs/heads/" + branch
	if out, err := exec.CommandContext(ctx, "git", "-C", p.RepoPath, "fetch", bundlePath, refspec).CombinedOutput(); err != nil {
		return fmt.Errorf("remote: import bundle into %s: %w: %s", branch, err, out)
	}
	return nil
}

// Push pushes branch from the canonical repository to the "origin" remote,
// exactly as every other backend's Publisher does: publication is identical
// once the bundle has landed a real branch in the canonical repository.
// Idempotent, matching engine.Publisher.Push's contract: pushing a branch
// whose remote tip already matches the local branch is not an error.
func (p *BundlePublisher) Push(ctx context.Context, _ execution.ExecutionEnvironment, branch string) error {
	if out, err := exec.CommandContext(ctx, "git", "-C", p.RepoPath, "push", "-u", "origin", branch).CombinedOutput(); err != nil {
		return fmt.Errorf("remote: push %s: %w: %s", branch, err, out)
	}
	return nil
}
