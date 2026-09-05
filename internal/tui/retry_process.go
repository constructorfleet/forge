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

// Retry spawns the detached child and waits for it to finish, capturing its
// stderr (issue #458: some refreshRetryBase failures leave no trace in the
// store, so the raw stderr is the only diagnostic for those). A non-nil
// error means the spawn never started or the child exited non-zero;
// RetryResult still carries whatever stderr the child produced.
func (p ProcessRetrier) Retry(executionID, issueID string) (RetryResult, error) {
	cmd := p.Command(executionID, issueID)
	stderr := textcap.NewTailWriter(clicommon.MaxCapturedOutputLen)
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return RetryResult{}, fmt.Errorf("tui: spawn retry child: %w", err)
	}
	waitErr := cmd.Wait()
	result := RetryResult{Stderr: stderr.String()}
	if waitErr == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, fmt.Errorf("retry child exited %d: %s", result.ExitCode, result.Stderr)
	}
	return result, fmt.Errorf("tui: wait for retry child: %w", waitErr)
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
	executable := p.Executable
	if executable == "" {
		executable = clicommon.SelfExecutable()
	}
	cmd := exec.CommandContext(context.Background(), executable, "retry", executionID+"/"+issueID,
		"--config", p.ConfigPath, "--db", p.DBPath)
	cmd.Dir = p.RepoRoot
	clicommon.ConfigureProcessGroup(cmd)
	return cmd
}
