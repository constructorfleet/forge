//go:build !darwin

package engine

import "fmt"

// darwinStartToken only runs on darwin. Elsewhere processStartToken falls
// through to /proc or ps.
func darwinStartToken(pid int) (string, error) {
	return "", fmt.Errorf("engine: darwinStartToken unsupported on this platform (pid %d)", pid)
}
