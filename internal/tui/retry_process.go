package tui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"

	"github.com/Teagan42/forge/internal/agent/clicommon"
	"github.com/Teagan42/forge/internal/textcap"
)

// ProcessRetrier is the production Retrier. It spawns a detached forge
// retry child rather than calling RetryIssue in-process (ADR 0031):
// RetryIssue ends in resumeIssue, full re-entry into workspace setup,
// rebase, the coding agent, the repair loop, gates, commit, and PR — the
// orchestrator this TUI observes. The child is built with a plain
// exec.Command, never exec.CommandContext, so no context this process holds
// can kill it once started, and ConfigureProcessGroup sets it to lead its
// own process group, the same convention internal/agent/clicommon uses for
// agent subprocesses, so it keeps running after the TUI quits.
type ProcessRetrier struct {
	// RepoRoot is the git top-level directory the child runs from (#459: a
	// retry run from any other directory can silently create an empty DB or
	// default a config it should have loaded).
	RepoRoot string

	// ConfigPath and DBPath are the absolute --config/--db paths passed to
	// the child, independent of whatever directory the child itself runs
	// from.
	ConfigPath string
	DBPath     string

	// Executable is the forge binary to spawn. Empty resolves via
	// os.Executable, the exact binary already running, falling back to the
	// bare "forge" name resolved via PATH. Tests override this to spawn a
	// stub instead of a real forge binary.
	Executable string
}

type detachedProcessConfig struct {
	repoRoot   string
	configPath string
	dbPath     string
	executable string
}

func (p ProcessRetrier) processConfig() detachedProcessConfig {
	return detachedProcessConfig{repoRoot: p.RepoRoot, configPath: p.ConfigPath, dbPath: p.DBPath, executable: p.Executable}
}

func (p ProcessResumer) processConfig() detachedProcessConfig {
	return detachedProcessConfig{repoRoot: p.RepoRoot, configPath: p.ConfigPath, dbPath: p.DBPath, executable: p.Executable}
}

func (c detachedProcessConfig) command(args ...string) *exec.Cmd {
	executable := c.executable
	if executable == "" {
		executable = clicommon.SelfExecutable()
	}
	args = append(args, "--config", c.configPath, "--db", c.dbPath)
	cmd := exec.CommandContext(context.Background(), executable, args...)
	cmd.Dir = c.repoRoot
	clicommon.ConfigureProcessGroup(cmd)
	return cmd
}

// ProcessResumer is the production Resumer. It starts a detached forge
// resume child, so the full execution can continue after the TUI exits.
type ProcessResumer struct {
	RepoRoot   string
	ConfigPath string
	DBPath     string
	Executable string
}

// Resume starts forge resume for an execution and captures the child's
// bounded stderr tail. The child owns all engineering work.
func (p ProcessResumer) Resume(executionID string) (RetryResult, error) {
	return runDetached("resume", p.Command(executionID))
}

// Command builds, but does not start, the detached resume child.
func (p ProcessResumer) Command(executionID string) *exec.Cmd {
	return p.processConfig().command("resume", executionID)
}

// Retry spawns the detached child and waits for it to finish, capturing its
// stderr (issue #458: some refreshRetryBase failures leave no trace in the
// store, so the raw stderr is the only diagnostic for those). A non-nil
// error means the spawn never started or the child exited non-zero;
// RetryResult still carries whatever stderr the child produced.
func (p ProcessRetrier) Retry(executionID, issueID string) (RetryResult, error) {
	return runDetached("retry", p.Command(executionID, issueID))
}

func runDetached(action string, cmd *exec.Cmd) (RetryResult, error) {
	stderr := textcap.NewTailWriter(clicommon.MaxCapturedOutputLen)
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return RetryResult{}, fmt.Errorf("tui: spawn %s child: %w", action, err)
	}
	waitErr := cmd.Wait()
	result := RetryResult{Stderr: stderr.String()}
	if waitErr == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, fmt.Errorf("%s child exited %d: %s", action, result.ExitCode, result.Stderr)
	}
	return result, fmt.Errorf("tui: wait for %s child: %w", action, waitErr)
}

// Command builds, but does not start, the detached retry child's exec.Cmd.
// Split out from Retry so a test can assert on its shape — Dir, Args, and
// the process-group SysProcAttr — without starting a real process.
//
// It is built with exec.CommandContext(context.Background(), ...), not the
// TUI's own context: ConfigureProcessGroup sets cmd.Cancel, and exec.Cmd
// requires Cancel to come from a CommandContext-built Cmd, but a Background
// context never completes, so that Cancel path never fires. Binding it to
// the TUI's own context instead would kill the child the moment the TUI
// quit, which defeats the entire point of detaching it.
func (p ProcessRetrier) Command(executionID, issueID string) *exec.Cmd {
	return p.processConfig().command("retry", executionID+"/"+issueID)
}
