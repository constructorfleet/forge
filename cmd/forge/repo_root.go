package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// discoverRepoRoot resolves the git top-level directory reachable from the
// current working directory (issue #459). Unlike os.Getwd(), it works from
// any subdirectory of a Forge repo checkout, not only from its root, and it
// fails loudly when the current directory is not inside a git work tree at
// all, rather than letting the caller treat an unrelated directory as the
// repo root.
func discoverRepoRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	out, err := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("%s is not inside a git repository; run this command from a Forge repo checkout", cwd)
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return "", fmt.Errorf("%s is not inside a git repository; run this command from a Forge repo checkout", cwd)
	}
	return root, nil
}
