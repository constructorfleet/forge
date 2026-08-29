package clicommon

import (
	"os/exec"
	"syscall"
	"time"
)

// killProcessGroupWaitDelay bounds how long Cmd.Wait waits, after Cancel
// signals the process group, for the stdout/stderr copy goroutines to see
// EOF — the same concern as internal/gate's waitDelay: Stderr here is an
// arbitrary io.Writer (a textcap.TailWriter, not *os.File), so exec.Cmd
// services it via a background copy goroutine that Wait ordinarily blocks
// on. A grandchild that inherited the write end of that pipe could hang
// Wait indefinitely even after the direct child is dead.
const killProcessGroupWaitDelay = 5 * time.Second

// ConfigureProcessGroup arranges for cmd, once started, to run as the
// leader of its own process group and for ctx cancellation (via
// exec.CommandContext) to kill that whole group rather than only cmd's
// direct child process (issue 33, "Agent runs need a timeout"). A CLI agent
// (e.g. `claude`) may spawn children of its own; without this, killing only
// the direct process on timeout/cancellation can leave those children
// running and holding the Workspace worktree open. Must be called before
// cmd.Start.
func ConfigureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = killProcessGroupWaitDelay
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative pid targets the whole process group cmd's Setpgid above
		// created (cmd.Process.Pid doubles as the group ID for the group
		// leader).
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
