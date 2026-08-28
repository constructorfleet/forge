// Package workspace creates and manages isolated Workspaces for Issue
// execution (see CONTEXT.md "Workspace"). The current mechanism is a Git
// worktree, but callers should speak in terms of Workspace, not worktree.
//
// The Manager never modifies the primary checkout: every operation it
// performs is either `git worktree add`, `git worktree remove`, a branch
// creation/deletion scoped to the worktree being managed, or a read-only
// inspection — the repository's current branch and working tree are left
// exactly as found.
package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/Teagan42/forge/internal/domain"
)

// ErrNotFound is returned by Validate when no Workspace has been created
// for the given executionID/issueID.
var ErrNotFound = errors.New("workspace: not found")

// validIDPattern restricts executionID and issueID to characters that can
// never be interpreted as a path-traversal or path-separator component,
// since both are joined directly into filesystem paths and git branch
// names.
var validIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// validateID rejects an executionID or issueID that is empty, contains
// characters outside validIDPattern, or could otherwise be used to escape
// the worktree root (e.g. "." or ".." or anything containing "..").
func validateID(kind, id string) error {
	if id == "" {
		return fmt.Errorf("workspace: %s must not be empty", kind)
	}
	if !validIDPattern.MatchString(id) {
		return fmt.Errorf(
			"workspace: %s %q contains invalid characters; only [A-Za-z0-9._-] are allowed", kind, id)
	}
	if id == "." || id == ".." || strings.Contains(id, "..") {
		return fmt.Errorf("workspace: %s %q is not a valid identifier", kind, id)
	}
	return nil
}

func validateIDs(executionID, issueID string) error {
	if err := validateID("executionID", executionID); err != nil {
		return err
	}
	if err := validateID("issueID", issueID); err != nil {
		return err
	}
	return nil
}

// CommandRunner executes a git subcommand with args, rooted at dir, and
// returns its captured stdout/stderr. Manager depends on this interface
// rather than os/exec directly so tests can inject hermetic or
// failure-injecting runners; production code uses the default
// execCommandRunner.
type CommandRunner interface {
	Run(ctx context.Context, dir string, args ...string) (stdout, stderr string, err error)
}

// Locker serializes short-lived repository metadata mutations such as git
// worktree add/remove and branch deletion across concurrent Executions.
type Locker interface {
	WithLock(ctx context.Context, resource string, fn func() error) error
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
	locker Locker

	// mu serializes every mutating sequence of git worktree/branch
	// operations against this repository. `git worktree add/remove` and
	// branch creation/deletion are not safe to run concurrently against
	// one repository (index.lock / .git/worktrees contention), and Create
	// and Cleanup are check-then-act (lookup, then mutate) so callers
	// creating/cleaning up distinct Workspaces concurrently would
	// otherwise race.
	mu sync.Mutex
}

// Option configures a Manager returned by NewManager.
type Option func(*Manager)

// WithWorktreeRoot overrides the default worktree root
// (".forge/worktrees"), relative to the repository root.
func WithWorktreeRoot(root string) Option {
	return func(m *Manager) { m.worktreeRoot = root }
}

// WithBranchTemplate overrides the default branch name template
// ("forge/{execution}/{issue}"). The template must contain the
// {execution} placeholder: without it, two Executions touching the same
// Issue would collide on one branch name, breaking Workspace isolation.
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

// WithLocker overrides the repository-scoped metadata lock implementation.
func WithLocker(locker Locker) Option {
	return func(m *Manager) { m.locker = locker }
}

// NewManager returns a Manager whose primary checkout is rooted at
// repoRoot. repoRoot must be the working directory of a Git repository (or
// worktree of one); it is never modified by Manager operations.
//
// NewManager rejects a branchTemplate (default or overridden via
// WithBranchTemplate) that lacks the {execution} placeholder, since that
// would let two Executions collide on one branch name.
func NewManager(repoRoot string, opts ...Option) (*Manager, error) {
	m := &Manager{
		repoRoot:       repoRoot,
		worktreeRoot:   ".forge/worktrees",
		branchTemplate: "forge/{execution}/{issue}",
		runner:         execCommandRunner{gitBin: "git"},
	}
	for _, opt := range opts {
		opt(m)
	}
	if !strings.Contains(m.branchTemplate, "{execution}") {
		return nil, fmt.Errorf(
			"workspace: branch template %q must contain the {execution} placeholder", m.branchTemplate)
	}

	// Resolve repoRoot to its canonical form up front (e.g. macOS's
	// /var -> /private/var symlink) so every path Manager builds from it
	// already matches what `git worktree list` reports git's own paths as
	// canonical. Without this, comparing a worktree path we just computed
	// against git's reported path can spuriously mismatch, and worse, that
	// mismatch is only masked when the target directory still exists
	// (samePath's own symlink-resolution fallback can't resolve a path
	// that's already been removed).
	if resolved, err := filepath.EvalSymlinks(m.repoRoot); err == nil {
		m.repoRoot = resolved
	}

	return m, nil
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

// wrapGitErr formats a failed git invocation into an actionable error
// identifying the command and its stderr (falling back to stdout if
// stderr was empty). It is the single source of truth for that error
// shape, shared by every call site that talks to git directly.
func wrapGitErr(args []string, stdout, stderr string, err error) error {
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		msg = strings.TrimSpace(stdout)
	}
	return fmt.Errorf("workspace: git %s: %w: %s", strings.Join(args, " "), err, msg)
}

// runGit runs a git subcommand rooted at dir and wraps any failure into an
// actionable error identifying the command and the git stderr output.
func (m *Manager) runGit(ctx context.Context, dir string, args ...string) (string, error) {
	stdout, stderr, err := m.runner.Run(ctx, dir, args...)
	if err != nil {
		return stdout, wrapGitErr(args, stdout, stderr, err)
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
// existing worktree's branch is not moved). To pick up a new base, callers
// must Cleanup the Workspace first — Cleanup deletes the branch along with
// the worktree, so a subsequent Create is free to recreate it from base.
func (m *Manager) Create(ctx context.Context, executionID, issueID, base string) (domain.Workspace, error) {
	if err := validateIDs(executionID, issueID); err != nil {
		return domain.Workspace{}, err
	}

	var ws domain.Workspace
	err := m.withGitMetadataLock(ctx, func() error {
		m.mu.Lock()
		defer m.mu.Unlock()

		path := m.path(executionID, issueID)
		branchName := m.branch(executionID, issueID)

		if existing, ok, err := m.lookupWorktree(ctx, path); err != nil {
			return err
		} else if ok {
			ws = domain.Workspace{IssueID: issueID, Path: path, Branch: existing.branch}
			return nil
		}

		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("workspace: create parent dir for %s: %w", path, err)
		}

		branchExists, err := m.branchExists(ctx, branchName)
		if err != nil {
			return err
		}

		// "--" terminates option parsing so a base/branch value that happens
		// to start with "-" is never misread as a git worktree add flag.
		var addArgs []string
		if branchExists {
			// Recovery path: the branch survived (e.g. the worktree directory
			// vanished without going through Cleanup) but no worktree is
			// currently registered for it. Reuse the branch as-is; base is
			// intentionally ignored here since the branch already has commits.
			addArgs = []string{"worktree", "add", "--", path, branchName}
		} else {
			addArgs = []string{"worktree", "add", "-b", branchName, "--", path, base}
		}
		if _, err := m.runGit(ctx, m.repoRoot, addArgs...); err != nil {
			return err
		}

		ws = domain.Workspace{IssueID: issueID, Path: path, Branch: branchName}
		return nil
	})
	if err != nil {
		return domain.Workspace{}, err
	}
	return ws, nil
}

// branchExists reports whether branchName already exists in the primary
// repository.
func (m *Manager) branchExists(ctx context.Context, branchName string) (bool, error) {
	args := []string{"rev-parse", "--verify", "--quiet", "refs/heads/" + branchName}
	stdout, stderr, err := m.runner.Run(ctx, m.repoRoot, args...)
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
	return false, wrapGitErr(args, stdout, stderr, err)
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

// Validate inspects the Workspace for executionID/issueID: registered with
// the primary repository and present on disk.
//
// It returns ErrNotFound (checkable with errors.Is) when no such Workspace
// has been created. Any other non-nil error means the Workspace is
// registered but unhealthy (e.g. its directory was removed out-of-band) or
// that inspection itself failed. On success it returns the Workspace.
func (m *Manager) Validate(ctx context.Context, executionID, issueID string) (domain.Workspace, error) {
	if err := validateIDs(executionID, issueID); err != nil {
		return domain.Workspace{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	path := m.path(executionID, issueID)

	entry, ok, err := m.lookupWorktree(ctx, path)
	if err != nil {
		return domain.Workspace{}, err
	}
	if !ok {
		return domain.Workspace{}, ErrNotFound
	}

	info, statErr := os.Stat(path)
	if statErr != nil {
		return domain.Workspace{}, fmt.Errorf(
			"workspace: registered worktree %s missing its directory: %w", path, statErr)
	}
	if !info.IsDir() {
		return domain.Workspace{}, fmt.Errorf(
			"workspace: registered worktree %s exists but is not a directory", path)
	}

	return domain.Workspace{IssueID: issueID, Path: path, Branch: entry.branch}, nil
}

// Cleanup removes the Workspace for executionID/issueID: its directory, its
// `git worktree` registration, and its branch. Cleaning up a Workspace that
// does not exist is not an error (any leftover directory at its path is
// still removed).
//
// Deleting the branch matters for correctness, not just tidiness: Create's
// idempotent-reuse path takes an existing branch as-is and ignores base, so
// an orphaned branch left behind by Cleanup would cause a later Create call
// with a new base to silently discard it.
func (m *Manager) Cleanup(ctx context.Context, executionID, issueID string) error {
	if err := validateIDs(executionID, issueID); err != nil {
		return err
	}

	return m.withGitMetadataLock(ctx, func() error {
		m.mu.Lock()
		defer m.mu.Unlock()

		path := m.path(executionID, issueID)

		entry, ok, err := m.lookupWorktree(ctx, path)
		if err != nil {
			return err
		}

		if !ok {
			// Nothing registered with git; still clear out any leftover
			// directory so a later Create isn't confused by stale contents.
			if err := os.RemoveAll(path); err != nil {
				return fmt.Errorf("workspace: remove directory %s: %w", path, err)
			}
			return nil
		}

		if _, err := m.runGit(ctx, m.repoRoot, "worktree", "remove", "--force", "--", path); err != nil {
			return err
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

		if entry.branch != "" {
			if _, err := m.runGit(ctx, m.repoRoot, "branch", "-D", "--", entry.branch); err != nil {
				return err
			}
		}

		return nil
	})
}

func (m *Manager) withGitMetadataLock(ctx context.Context, fn func() error) error {
	if m.locker == nil {
		return fn()
	}
	return m.locker.WithLock(ctx, "git-metadata", fn)
}
