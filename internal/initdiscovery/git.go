package initdiscovery

import (
	"fmt"
	"net/url"
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

// detectTracker resolves the issue tracker from the "origin" remote URL and
// composes it onto cfg. It recognizes github, gitlab, and gitea hosts. A github
// remote keeps cfg at its config.Default() github composition. A gitlab
// remote (gitlab.com, or a host whose name contains "gitlab") switches the
// whole capability composition to gitlab and fills the project path -- and,
// for a self-managed instance, the base URL -- from the remote. A gitea remote
// (codeberg.org, or a host whose name contains "gitea") switches the whole
// composition to gitea and fills the project path and the base URL from the
// remote. An unrecognized host leaves the github default in place and returns a
// Note, because forge cannot confirm the tracker from an unknown host name.
func detectTracker(dir string, cfg *config.Config) *Note {
	remote, err := runGit(dir, "remote", "get-url", "origin")
	if err != nil || remote == "" {
		return &Note{
			Field:   "tracker.type",
			Message: "no git remote \"origin\" found; defaulting to github, verify manually",
		}
	}

	host, path, ok := parseRemoteHostPath(remote)
	if !ok {
		return &Note{
			Field:   "tracker.type",
			Message: fmt.Sprintf("could not parse git remote \"origin\" (%s); defaulting to github, verify manually", remote),
		}
	}

	switch {
	case strings.Contains(host, "github"):
		cfg.Tracker.Type = "github"
		return nil
	case host == "gitlab.com" || strings.Contains(host, "gitlab"):
		applyGitLabComposition(cfg, host, path)
		// The project path is inferred from the remote, so the human must
		// confirm it. A self-managed GitLab instance can use any host name,
		// so forge cannot detect gitlab there (see config.GitLabConfig);
		// only a recognizable "gitlab" host reaches this branch.
		return &Note{
			Field:   "tracker.gitlab.project",
			Message: fmt.Sprintf("inferred %q from git remote \"origin\" (%s); verify the path with namespace is correct", path, remote),
		}
	case host == "codeberg.org" || strings.Contains(host, "gitea"):
		applyGiteaComposition(cfg, host, path)
		// The project path is inferred from the remote, so the human must
		// confirm it. A self-managed Gitea instance can use any host name, so
		// forge cannot detect gitea there (see config.GiteaConfig); only a
		// recognizable "gitea" host, or codeberg.org, reaches this branch.
		return &Note{
			Field:   "tracker.gitea.project",
			Message: fmt.Sprintf("inferred %q from git remote \"origin\" (%s); verify the owner/repo is correct", path, remote),
		}
	default:
		return &Note{
			Field:   "tracker.type",
			Message: fmt.Sprintf("git remote \"origin\" (%s) is not a recognized github, gitlab, or gitea host; defaulting to github, verify manually", remote),
		}
	}
}

// applyGiteaComposition switches every capability on cfg to gitea and sets the
// project the tracker reads and writes plus the base URL. Gitea has no fixed
// host, so it always sets the base URL from the remote host, including for
// codeberg.org (see config.GiteaConfig.BaseURL).
func applyGiteaComposition(cfg *config.Config, host, path string) {
	cfg.Provider = "gitea"
	cfg.Tracker.Type = "gitea"
	cfg.Tracker.Provider = "gitea"
	cfg.SCM.Type = "gitea"
	cfg.CI.Type = "gitea"
	cfg.Tracker.Gitea.Project = path
	cfg.Tracker.Gitea.BaseURL = "https://" + host
}

// applyGitLabComposition switches every capability on cfg to gitlab and sets
// the project the tracker reads and writes. It sets the base URL only for a
// self-managed instance (a host other than gitlab.com); gitlab.com needs no
// base URL (see config.GitLabConfig.BaseURL).
func applyGitLabComposition(cfg *config.Config, host, path string) {
	cfg.Provider = "gitlab"
	cfg.Tracker.Type = "gitlab"
	cfg.Tracker.Provider = "gitlab"
	cfg.SCM.Type = "gitlab"
	cfg.CI.Type = "gitlab"
	cfg.Tracker.GitLab.Project = path
	if host != "gitlab.com" {
		cfg.Tracker.GitLab.BaseURL = "https://" + host
	}
}

// parseRemoteHostPath splits a git remote URL into its host and its
// project path (the "group/project" part, without a ".git" suffix or
// surrounding slashes). It handles both the scp-like SSH syntax
// ("git@host:group/project.git") and a scheme URL
// ("https://host/group/project.git", "ssh://git@host/group/project.git").
// It returns ok=false when either the host or the path is empty.
func parseRemoteHostPath(remote string) (host, path string, ok bool) {
	s := strings.TrimSpace(remote)
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return "", "", false
		}
		host = u.Hostname()
		path = u.Path
	} else {
		// scp-like syntax: [user@]host:path, with no scheme and no "/"
		// before the first ":".
		if at := strings.LastIndex(s, "@"); at >= 0 {
			s = s[at+1:]
		}
		colon := strings.Index(s, ":")
		if colon < 0 {
			return "", "", false
		}
		host = s[:colon]
		path = s[colon+1:]
	}

	host = strings.ToLower(strings.TrimSpace(host))
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	if host == "" || path == "" {
		return "", "", false
	}
	return host, path, true
}
