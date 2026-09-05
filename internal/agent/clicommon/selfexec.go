package clicommon

import "os"

// SelfExecutable resolves the currently-running forge binary's absolute path
// (os.Executable), so a component that spawns a child forge process spawns
// the exact binary already running, falling back to the bare "forge" name
// (resolved via PATH by the child's own subprocess spawn) if that lookup
// fails.
func SelfExecutable() string {
	path, err := os.Executable()
	if err != nil {
		return "forge"
	}
	return path
}
