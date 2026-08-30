// Package lsp maps a workspace's detected languages to the Language Servers
// that may run for it (see CONTEXT.md "Language Server Registry"). It reuses
// the language signal Forge already computes in
// agent.RepositoryContext.Languages rather than running its own detection.
package lsp

import (
	"maps"
	"strings"

	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/semantic/lspdriver"
)

// ServerSpec is one Language Server Registry entry: the command that starts
// the Language Server for a language, plus its declarative ServerProfile
// (see lspdriver.ServerProfile). It carries no capability information — a
// server's capabilities are learned later from its LSP initialize handshake,
// never from the registry.
type ServerSpec struct {
	Command []string
	Profile lspdriver.ServerProfile
}

// Registry is the Language Server Registry: a language identifier mapped to
// the ServerSpec that serves it.
type Registry map[string]ServerSpec

// builtinServers seeds the Language Server Registry with Forge's v1
// defaults: the four supported languages, each with the ServerProfile its
// server's markdown/symbol quirks require (see ADR 0016). "javascript" is
// the single key for the whole Node/JS/TS family — repocontext keeps
// emitting "JavaScript" for package.json, and typescript-language-server
// serves .js and .ts alike.
var builtinServers = Registry{
	"go": {
		Command: []string{"gopls"},
		Profile: lspdriver.ServerProfile{HoverStyle: lspdriver.HoverStyleFirstFence},
	},
	"rust": {
		Command: []string{"rust-analyzer"},
		Profile: lspdriver.ServerProfile{HoverStyle: lspdriver.HoverStyleRustTwoFence},
	},
	"python": {
		Command: []string{"pyright-langserver", "--stdio"},
		Profile: lspdriver.ServerProfile{
			HoverStyle:         lspdriver.HoverStylePyrightAnnotated,
			DropSymbolChildren: true,
		},
	},
	"javascript": {
		Command: []string{"typescript-language-server", "--stdio"},
		Profile: lspdriver.ServerProfile{HoverStyle: lspdriver.HoverStyleFirstFence},
	},
}

// NewRegistry builds the Language Server Registry from Forge's built-in
// defaults, merged over/extended by cfg.Servers — a configured command for a
// language merges into the existing row, preserving its built-in Profile;
// a configured language absent from the built-ins is added with the zero
// ServerProfile. Configuration alone never gates which servers run against
// a workspace; see Detect.
func NewRegistry(cfg config.LSPConfig) Registry {
	registry := make(Registry, len(builtinServers)+len(cfg.Servers))
	maps.Copy(registry, builtinServers)
	for language, server := range cfg.Servers {
		key := strings.ToLower(language)
		spec := registry[key]
		spec.Command = server.Command
		registry[key] = spec
	}
	return registry
}

// DetectedServer is the identity of a Language Server that may run for a
// detected language: which language it serves and the command that starts
// it. It carries no capability flags — those come from the server's own LSP
// initialize handshake.
type DetectedServer struct {
	Language string
	Command  []string
}

// Detect maps languages (as reported by agent.RepositoryContext.Languages)
// through registry to produce the workspace's Detected Servers. Detection
// gates which servers may run: a registry entry for a language absent from
// languages is never returned, so configuring a server cannot force-start it
// for an undetected language.
func Detect(languages []string, registry Registry) []DetectedServer {
	var detected []DetectedServer
	for _, language := range languages {
		spec, ok := registry[strings.ToLower(language)]
		if !ok {
			continue
		}
		detected = append(detected, DetectedServer{Language: strings.ToLower(language), Command: spec.Command})
	}
	return detected
}
