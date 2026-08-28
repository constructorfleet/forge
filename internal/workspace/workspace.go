// Package workspace creates and manages isolated Workspaces for Issue
// execution (see CONTEXT.md "Workspace"). The current mechanism is a Git
// worktree, but callers should speak in terms of Workspace, not worktree.
//
// The Manager never modifies the primary checkout: every operation it
// performs is either `git worktree add`, `git worktree remove`, or a branch
// creation scoped to the worktree being created — the repository's current
// branch and working tree are left exactly as found.
package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Teagan42/forge/internal/domain"
)

// CommandRunner executes a git subcommand with args, rooted at dir, and
// returns its captured stdout/stderr. Manager depends on this interface
// rather than os/exec directly so tests can inject hermetic or
// failure-injecting runners; production code uses the default
// execCommandRunner.
type CommandRunner interface {
	Run(ctx context.Context, dir string, args ...string) (stdout, stderr string, err error)
}

// execCommandRunner is the default CommandRunner, backed by os/exec.
type execCommandRunner struct {
	gitBin string
}

func (r execCommandRunner) Run(ctx context.Context, dir string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, r.gitBin, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// Manager creates and manages Workspaces (Git worktrees) rooted under a
// primary checkout. It never modifies that primary checkout.
type Manager struct {
	// repoRoot is the primary checkout's working directory. git worktree
	// commands are always run from here; individual Workspace operations
	// target the worktree's own path, not repoRoot's working tree.
	repoRoot string

	// worktreeRoot is the path, relative to repoRoot, under which
	// per-Execution/per-Issue worktrees are created. Mirrors
	// config.GitConfig.WorktreeRoot.
	worktreeRoot string

	// branchTemplate formats a branch name from an execution ID and Issue
	// ID using the {execution} and {issue} placeholders.
	branchTemplate string

	runner CommandRunner
}

// Option configures a Manager returned by NewManager.
type Option func(*Manager)

// WithWorktreeRoot overrides the default worktree root
// (".forge/worktrees"), relative to the repository root.
func WithWorktreeRoot(root string) Option {
	return func(m *Manager) { m.worktreeRoot = root }
}

// WithBranchTemplate overrides the default branch name template
// ("forge/{execution}/{issue}").
func WithBranchTemplate(tmpl string) Option {
	return func(m *Manager) { m.branchTemplate = tmpl }
}

// WithRunner overrides the CommandRunner used to invoke git, primarily for
// tests that need to inject failures without a real git binary.
func WithRunner(runner CommandRunner) Option {
	return func(m *Manager) { m.runner = runner }
}

// WithGitBinary overrides the git executable name/path (default "git").
func WithGitBinary(bin string) Option {
	return func(m *Manager) { m.runner = execCommandRunner{gitBin: bin} }
}

// NewManager returns a Manager whose primary checkout is rooted at
// repoRoot. repoRoot must be the working directory of a Git repository (or
// worktree of one); it is never modified by Manager operations.
func NewManager(repoRoot string, opts ...Option) *Manager {
	m := &Manager{
		repoRoot:       repoRoot,
		worktreeRoot:   ".forge/worktrees",
		branchTemplate: "forge/{execution}/{issue}",
		runner:         execCommandRunner{gitBin: "git"},
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// path returns the absolute Workspace directory for executionID/issueID.
func (m *Manager) path(executionID, issueID string) string {
	root := m.worktreeRoot
	if !filepath.IsAbs(root) {
		root = filepath.Join(m.repoRoot, root)
	}
	return filepath.Join(root, executionID, issueID)
}

// branch returns the branch name for executionID/issueID per
// branchTemplate.
func (m *Manager) branch(executionID, issueID string) string {
	name := m.branchTemplate
	name = strings.ReplaceAll(name, "{execution}", executionID)
	name = strings.ReplaceAll(name, "{issue}", issueID)
	return name
}

// runGit runs a git subcommand rooted at dir and wraps any failure into an
// actionable error identifying the command and the git stderr output.
func (m *Manager) runGit(ctx context.Context, dir string, args ...string) (string, error) {
	stdout, stderr, err := m.runner.Run(ctx, dir, args...)
	if err != nil {
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = strings.TrimSpace(stdout)
		}
		return stdout, fmt.Errorf("workspace: git %s: %w: %s", strings.Join(args, " "), err, msg)
	}
	return stdout, nil
}

// Create creates (or, if it already exists, reuses) the Workspace for
// executionID/issueID, checking out a new branch from base. base is the
// revision captured per-Worker at the Issue's READY transition — callers
// must not assume it equals the Execution's starting base revision, since a
// dependency-blocked Issue's Worker captures a newer base once its
// prerequisites have merged.
//
// Create is idempotent: calling it again for the same executionID/issueID
// returns the existing Workspace without error, regardless of base (the
// existing worktree's branch is not moved).
func (m *Manager) Create(ctx context.Context, executionID, issueID, base string) (domain.Workspace, error) {
	path := m.path(executionID, issueID)
	branchName := m.branch(executionID, issueID)

	if existing, ok, err := m.lookupWorktree(ctx, path); err != nil {
		return domain.Workspace{}, err
	} else if ok {
		return domain.Workspace{IssueID: issueID, Path: path, Branch: existing.branch}, nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return domain.Workspace{}, fmt.Errorf("workspace: create parent dir for %s: %w", path, err)
	}

	branchExists, err := m.branchExists(ctx, branchName)
	if err != nil {
		return domain.Workspace{}, err
	}

	var addArgs []string
	if branchExists {
		addArgs = []string{"worktree", "add", path, branchName}
	} else {
		addArgs = []string{"worktree", "add", "-b", branchName, path, base}
	}
	if _, err := m.runGit(ctx, m.repoRoot, addArgs...); err != nil {
		return domain.Workspace{}, err
	}

	return domain.Workspace{IssueID: issueID, Path: path, Branch: branchName}, nil
}

// branchExists reports whether branchName already exists in the primary
// repository.
func (m *Manager) branchExists(ctx context.Context, branchName string) (bool, error) {
	_, stderr, err := m.runner.Run(ctx, m.repoRoot, "rev-parse", "--verify", "--quiet", "refs/heads/"+branchName)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		// rev-parse --verify --quiet exits non-zero with empty output when
		// the ref does not exist; that is not itself a failure worth
		// reporting.
		return false, nil
	}
	return false, fmt.Errorf("workspace: git rev-parse --verify %s: %w: %s", branchName, err, strings.TrimSpace(stderr))
}

// worktreeEntry is one parsed entry from `git worktree list --porcelain`.
type worktreeEntry struct {
	path   string
	branch string
}

// listWorktrees returns every worktree registered against the primary
// repository, parsed from `git worktree list --porcelain`.
func (m *Manager) listWorktrees(ctx context.Context) ([]worktreeEntry, error) {
	out, err := m.runGit(ctx, m.repoRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}

	var entries []worktreeEntry
	var cur worktreeEntry
	flush := func() {
		if cur.path != "" {
			entries = append(entries, cur)
		}
		cur = worktreeEntry{}
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "worktree "):
			cur.path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			cur.branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		}
	}
	flush()
	return entries, nil
}

// lookupWorktree reports whether a worktree is already registered at path.
func (m *Manager) lookupWorktree(ctx context.Context, path string) (worktreeEntry, bool, error) {
	entries, err := m.listWorktrees(ctx)
	if err != nil {
		return worktreeEntry{}, false, err
	}
	for _, e := range entries {
		if samePath(e.path, path) {
			return e, true, nil
		}
	}
	return worktreeEntry{}, false, nil
}

func samePath(a, b string) bool {
	ca, err1 := filepath.EvalSymlinks(a)
	cb, err2 := filepath.EvalSymlinks(b)
	if err1 != nil || err2 != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ca == cb
}

// Validate reports whether the Workspace for executionID/issueID exists and
// is a healthy Git worktree: registered with the primary repository and
// present on disk. ok is false (with a nil error) when no such Workspace
// has been created. A non-nil error indicates the Workspace is registered
// but unhealthy (e.g. its directory was removed out-of-band) or that
// inspection itself failed.
func (m *Manager) Validate(ctx context.Context, executionID, issueID string) (domain.Workspace, bool, error) {
	path := m.path(executionID, issueID)

	entry, ok, err := m.lookupWorktree(ctx, path)
	if err != nil {
		return domain.Workspace{}, false, err
	}
	if !ok {
		return domain.Workspace{}, false, nil
	}

	info, statErr := os.Stat(path)
	if statErr != nil || !info.IsDir() {
		return domain.Workspace{}, false, fmt.Errorf(
			"workspace: registered worktree %s missing its directory: %w", path, statErr)
	}

	return domain.Workspace{IssueID: issueID, Path: path, Branch: entry.branch}, true, nil
}

// Cleanup removes the Workspace for executionID/issueID: both its directory
// and its `git worktree` registration. Cleaning up a Workspace that does
// not exist is not an error.
func (m *Manager) Cleanup(ctx context.Context, executionID, issueID string) error {
	path := m.path(executionID, issueID)

	_, ok, err := m.lookupWorktree(ctx, path)
	if err != nil {
		return err
	}
	if ok {
		if _, err := m.runGit(ctx, m.repoRoot, "worktree", "remove", "--force", path); err != nil {
			return err
		}
	}

	// Belt-and-suspenders: `git worktree remove` deletes the directory
	// itself, but if it was already gone (e.g. removed out-of-band) or
	// left behind for any reason, make sure it's gone.
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("workspace: remove directory %s: %w", path, err)
	}

	if _, err := m.runGit(ctx, m.repoRoot, "worktree", "prune"); err != nil {
		return err
	}
	return nil
}
