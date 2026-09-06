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

// NewRegistry builds the Language Server Registry from the Language-to-LSP
// Table (Languages): for each language it runs ProbeBinaries and stores the
// candidate binary found on PATH, falling back to the first (preferred)
// candidate when none resolves — reproducing Forge's original defaults on a
// machine with no Language Server installed. The result is merged
// over/extended by cfg.Servers — a configured command for a language merges
// into the existing row, preserving its resolved Profile; a configured
// language absent from Languages is added with the zero ServerProfile.
// Configuration alone never gates which servers run against a workspace;
// see Detect.
func NewRegistry(cfg config.LSPConfig) Registry {
	registry := make(Registry, len(Languages)+len(cfg.Servers))
	for _, spec := range Languages {
		if spec.RegistryID == "" || len(spec.Binaries) == 0 {
			continue
		}
		candidate := spec.Binaries[0]
		if found, ok := ProbeBinaries(spec); ok {
			candidate = found
		}
		registry[spec.RegistryID] = ServerSpec{
			Command: candidateCommand(candidate),
			Profile: candidate.Profile,
		}
	}
	for language, server := range cfg.Servers {
		key := strings.ToLower(language)
		spec := registry[key]
		spec.Command = server.Command
		registry[key] = spec
	}
	return registry
}

// candidateCommand returns the argv that launches c: its explicit Command,
// or a single bare argument of its Name when Command is unset.
func candidateCommand(c BinaryCandidate) []string {
	if len(c.Command) > 0 {
		return c.Command
	}
	return []string{c.Name}
}

// builtinExtensions is the file-extension -> language table the
// multiplexing MCP server routes tool calls through (ADR 0016): the whole
// Node/JS/TS family collapses onto the single "javascript" registry key,
// since typescript-language-server serves .js and .ts alike.
var builtinExtensions = map[string]string{
	".go":  "go",
	".rs":  "rust",
	".py":  "python",
	".js":  "javascript",
	".jsx": "javascript",
	".ts":  "javascript",
	".tsx": "javascript",
	".mjs": "javascript",
	".cjs": "javascript",
}

// Extensions builds the file-extension -> language table from Forge's
// built-in defaults, overridden and extended by cfg.Extensions. Keys are
// normalized to lowercase with a leading dot, so an operator may write
// either "mjs" or ".MJS". The languages it names are the registry's, so a
// row for a language no server serves simply routes nowhere.
func Extensions(cfg config.LSPConfig) map[string]string {
	extensions := make(map[string]string, len(builtinExtensions)+len(cfg.Extensions))
	maps.Copy(extensions, builtinExtensions)
	for ext, language := range cfg.Extensions {
		extensions[normalizeExtension(ext)] = strings.ToLower(language)
	}
	return extensions
}

// normalizeExtension lowercases ext and ensures exactly one leading dot.
func normalizeExtension(ext string) string {
	ext = strings.ToLower(ext)
	if ext == "" || strings.HasPrefix(ext, ".") {
		return ext
	}
	return "." + ext
}

// DetectedServer is the identity of a Language Server that may run for a
// detected language: which language it serves, the command that starts it,
// and the declarative ServerProfile its quirks require. It carries no
// capability flags — those come from the server's own LSP initialize
// handshake.
type DetectedServer struct {
	Language string
	Command  []string
	Profile  lspdriver.ServerProfile
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
		detected = append(detected, DetectedServer{
			Language: strings.ToLower(language),
			Command:  spec.Command,
			Profile:  spec.Profile,
		})
	}
	return detected
}
