//go:build !darwin

package engine

// platformStartToken is nil on a non-darwin platform, so processStartToken
// falls through to /proc or ps. See issue 561.
var platformStartToken func(pid int) (string, error)
