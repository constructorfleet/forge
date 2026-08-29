// Package lsp maps a workspace's detected languages to the Language Servers
// that may run for it (see CONTEXT.md "Language Server Registry"). It reuses
// the language signal Forge already computes in
// agent.RepositoryContext.Languages rather than running its own detection.
package lsp

import (
	"strings"

	"github.com/Teagan42/forge/internal/config"
)

// ServerSpec is one Language Server Registry entry: the command that starts
// the Language Server for a language. It carries no capability information —
// a server's capabilities are learned later from its LSP initialize
// handshake, never from the registry.
type ServerSpec struct {
	Command []string
}

// Registry is the Language Server Registry: a language identifier mapped to
// the ServerSpec that serves it.
type Registry map[string]ServerSpec

// builtinServers seeds the Language Server Registry with Forge's defaults.
// v1 ships exactly one entry: Go, served by gopls.
var builtinServers = Registry{
	"go": {Command: []string{"gopls"}},
}

// NewRegistry builds the Language Server Registry from Forge's built-in
// defaults, merged over/extended by cfg.Servers — a configured command for a
// language replaces the built-in one; a configured language absent from the
// built-ins is added. Configuration alone never gates which servers run
// against a workspace; see Detect.
func NewRegistry(cfg config.LSPConfig) Registry {
	registry := make(Registry, len(builtinServers)+len(cfg.Servers))
	for language, spec := range builtinServers {
		registry[language] = spec
	}
	for language, server := range cfg.Servers {
		registry[strings.ToLower(language)] = ServerSpec{Command: server.Command}
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
