//go:build darwin

package engine

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// darwinStartToken reads pid's start time from the kernel's kinfo_proc
// through sysctl. The value carries microsecond precision, unlike ps
// -o lstart=, which only has one-second granularity and can give the same
// token to two different processes that reuse a pid within the same second.
// See issue 561.
func darwinStartToken(pid int) (string, error) {
	if pid <= 0 {
		return "", fmt.Errorf("engine: invalid pid %d", pid)
	}
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", fmt.Errorf("engine: sysctl kern.proc.pid %d: %w", pid, err)
	}
	start := info.Proc.P_starttime
	if start.Sec == 0 && start.Usec == 0 {
		return "", fmt.Errorf("engine: no start time for pid %d", pid)
	}
	return fmt.Sprintf("%d.%06d", start.Sec, start.Usec), nil
}

// platformStartToken is darwinStartToken on darwin. processStartToken calls
// it through this var, not the function directly, so staticcheck cannot
// prove across build tags that the non-darwin stub always errors. See issue
// 561.
var platformStartToken = darwinStartToken
