// Package clitoken resolves tracker credentials from environment variables or
// the provider's existing command line authentication.
package clitoken

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Source string

const (
	SourceEnv  Source = "env"
	SourceCLI  Source = "cli"
	SourceNone Source = "none"
)

var commandContext = exec.CommandContext
var userConfigDir = os.UserConfigDir

// Resolve returns a token and its source. It never returns a CLI error.
func Resolve(parent context.Context, provider, envVar, targetURL string) (string, Source) {
	if token := os.Getenv(envVar); token != "" {
		return token, SourceEnv
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	switch provider {
	case "github":
		args := []string{"auth", "token"}
		if h := host(targetURL); h != "" && h != "github.com" {
			args = append(args, "--hostname", h)
		}
		return commandToken(ctx, "gh", args...)
	case "gitlab":
		args := []string{"auth", "token"}
		if h := host(targetURL); h != "" && h != "gitlab.com" {
			args = append(args, "--hostname", h)
		}
		return commandToken(ctx, "glab", args...)
	case "gitea":
		return teaToken(targetURL)
	default:
		return "", SourceNone
	}
}

func commandToken(ctx context.Context, name string, args ...string) (string, Source) {
	args = appendNonEmpty(nil, args...)
	output, err := commandContext(ctx, name, args...).Output()
	if err != nil {
		return "", SourceNone
	}
	if token := strings.TrimSpace(string(output)); token != "" {
		return token, SourceCLI
	}
	return "", SourceNone
}

func appendNonEmpty(dst []string, values ...string) []string {
	for _, value := range values {
		if value != "" {
			dst = append(dst, value)
		}
	}
	return dst
}

func host(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

type teaConfig struct {
	Logins []teaLogin `yaml:"logins"`
}
type teaLogin struct {
	URL     string `yaml:"url"`
	Token   string `yaml:"token"`
	Default bool   `yaml:"default"`
}

func teaToken(targetURL string) (string, Source) {
	dir, err := userConfigDir()
	if err != nil {
		return "", SourceNone
	}
	data, err := os.ReadFile(filepath.Join(dir, "tea", "config.yml"))
	if err != nil {
		return "", SourceNone
	}
	var config teaConfig
	if yaml.Unmarshal(data, &config) != nil {
		return "", SourceNone
	}
	if len(config.Logins) == 0 {
		return "", SourceNone
	}
	want := host(targetURL)
	var selected *teaLogin
	for i := range config.Logins {
		if host(config.Logins[i].URL) == want && want != "" {
			selected = &config.Logins[i]
			break
		}
	}
	if selected == nil {
		for i := range config.Logins {
			if config.Logins[i].Default {
				selected = &config.Logins[i]
				break
			}
		}
	}
	if selected == nil && len(config.Logins) == 1 {
		selected = &config.Logins[0]
	}
	if selected != nil && strings.TrimSpace(selected.Token) != "" {
		return strings.TrimSpace(selected.Token), SourceCLI
	}
	return "", SourceNone
}
