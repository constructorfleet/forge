package initdiscovery

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/Teagan42/forge/internal/config"
)

// runGit runs `git <args...>` with dir as the working directory (via `git
// -C dir`, so tests can point at temp fixture repos without changing the
// process's cwd) and returns trimmed stdout.
func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// detectBaseBranch resolves the base branch Workers should target.
//
// Priority: the remote's recorded HEAD (refs/remotes/origin/HEAD, the most
// authoritative signal — what the remote itself considers default) > the
// local repo's init.defaultBranch config > a local "main" or "master"
// branch, in that order. If none resolve, the config.Default() base
// ("origin/main") is kept and a Note is returned so the generated file
// marks it as unverified rather than silently presenting it as detected.
func detectBaseBranch(dir string) (string, *Note) {
	if ref, err := runGit(dir, "symbolic-ref", "refs/remotes/origin/HEAD"); err == nil {
		if branch := lastPathElem(ref); branch != "" {
			return "origin/" + branch, nil
		}
	}

	// --local restricts this to the repo's own .git/config, not the
	// operator's global/system git config — a global init.defaultBranch
	// says nothing about this specific repository's convention.
	if branch, err := runGit(dir, "config", "--local", "init.defaultBranch"); err == nil && branch != "" {
		return "origin/" + branch, nil
	}

	for _, candidate := range []string{"main", "master"} {
		if _, err := runGit(dir, "show-ref", "--verify", "--quiet", "refs/heads/"+candidate); err == nil {
			return "origin/" + candidate, nil
		}
	}

	return config.Default().Git.Base, &Note{
		Field:   "git.base",
		Message: "could not detect a base branch (no origin/HEAD, init.defaultBranch, or local main/master); verify manually",
	}
}

// lastPathElem returns the final "/"-separated element of s, e.g.
// "refs/remotes/origin/main" -> "main".
func lastPathElem(s string) string {
	parts := strings.Split(s, "/")
	return parts[len(parts)-1]
}

// detectTracker resolves the issue tracker from the "origin" remote URL.
// forge currently only supports github, so this only ever confirms github
// (leaving cfg.Tracker.Type at its config.Default() value of "github") or
// flags that the remote could not be confirmed as github.
func detectTracker(dir string, cfg *config.Config) *Note {
	url, err := runGit(dir, "remote", "get-url", "origin")
	if err != nil || url == "" {
		return &Note{
			Field:   "tracker.type",
			Message: "no git remote \"origin\" found; defaulting to github, verify manually",
		}
	}

	if strings.Contains(url, "github.com") {
		cfg.Tracker.Type = "github"
		return nil
	}

	return &Note{
		Field:   "tracker.type",
		Message: fmt.Sprintf("git remote \"origin\" (%s) does not look like github.com; forge currently only supports github, verify manually", url),
	}
}
