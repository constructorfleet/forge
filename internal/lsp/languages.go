package lsp

import "strings"

// BinaryCandidate is one Language Server binary Forge may run for a
// language, in the order Forge tries it, with the static install hint to
// show when the binary is not on PATH.
type BinaryCandidate struct {
	Name        string
	InstallHint string
}

// LanguageSpec is one row of the Language-to-LSP Table: a language, the
// manifest filenames and fallback file extensions that detect it (see
// Scan), the ordered candidate Language Server binaries Forge tries for
// it, and the RegistryID that names it in the Language Server Registry
// (Registry, Detect).
type LanguageSpec struct {
	Language           string
	RegistryID         string
	ManifestFilenames  []string
	FallbackExtensions []string
	Binaries           []BinaryCandidate
}

// Languages is the static, ordered Language-to-LSP Table: the sole source
// of truth for which languages Forge supports, the ordered candidate
// binaries it tries for each one, each binary's install hint, and the
// RegistryID that names the language in the Language Server Registry
// (Registry, Detect). No configuration file, environment variable, or
// flag overrides this order or pins a binary.
//
// Wiring a real caller — Scan's manifest/extension inputs, or
// initdiscovery's hand-rolled language table — to read from Languages
// instead of a second hand-written table is deferred to a follow-up
// ticket.
//
// detect.go's Registry (seeded from builtinServers) governs which
// Language Server actually starts for a detected language, keyed by
// RegistryID rather than this table's display names, and it can name
// different commands (for example pyright-langserver --stdio for Python,
// versus this table's bare pyright). Registry serves only Go,
// TypeScript/JavaScript, Python, and Rust today; a RegistryID with no
// matching Registry entry simply has no server yet (see LanguageID).
var Languages = []LanguageSpec{
	{
		Language:           "Go",
		RegistryID:         "go",
		ManifestFilenames:  []string{"go.mod"},
		FallbackExtensions: []string{".go"},
		Binaries: []BinaryCandidate{
			{Name: "gopls", InstallHint: "go install golang.org/x/tools/gopls@latest"},
		},
	},
	{
		Language:           "TypeScript/JavaScript",
		RegistryID:         "javascript",
		ManifestFilenames:  []string{"package.json"},
		FallbackExtensions: []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"},
		Binaries: []BinaryCandidate{
			{Name: "typescript-language-server", InstallHint: "npm install -g typescript-language-server typescript"},
		},
	},
	{
		Language:           "Python",
		RegistryID:         "python",
		ManifestFilenames:  []string{"pyproject.toml", "requirements.txt", "setup.py"},
		FallbackExtensions: []string{".py"},
		Binaries: []BinaryCandidate{
			{Name: "pyright", InstallHint: "npm install -g pyright"},
			{Name: "pylsp", InstallHint: "pip install python-lsp-server"},
		},
	},
	{
		Language:           "Rust",
		RegistryID:         "rust",
		ManifestFilenames:  []string{"Cargo.toml"},
		FallbackExtensions: []string{".rs"},
		Binaries: []BinaryCandidate{
			{Name: "rust-analyzer", InstallHint: "rustup component add rust-analyzer"},
		},
	},
	{
		Language:           "C/C++",
		RegistryID:         "cpp",
		ManifestFilenames:  []string{"CMakeLists.txt", "Makefile"},
		FallbackExtensions: []string{".c", ".h", ".cpp", ".cc", ".cxx", ".hpp"},
		Binaries: []BinaryCandidate{
			{Name: "clangd", InstallHint: "brew install llvm"},
		},
	},
	{
		Language:           "Java",
		RegistryID:         "java",
		ManifestFilenames:  []string{"pom.xml", "build.gradle", "build.gradle.kts"},
		FallbackExtensions: []string{".java"},
		Binaries: []BinaryCandidate{
			{Name: "jdtls", InstallHint: "brew install jdtls"},
		},
	},
	{
		Language:           "Ruby",
		RegistryID:         "ruby",
		ManifestFilenames:  []string{"Gemfile"},
		FallbackExtensions: []string{".rb"},
		Binaries: []BinaryCandidate{
			{Name: "solargraph", InstallHint: "gem install solargraph"},
			{Name: "ruby-lsp", InstallHint: "gem install ruby-lsp"},
		},
	},
}

// LanguageID returns the Language Server Registry key for a
// Language-to-LSP Table display name (Languages), falling back to a
// plain lowercase of the name for a language the table does not list (no
// registry entry can match it either way).
func LanguageID(displayName string) string {
	for _, spec := range Languages {
		if spec.Language == displayName {
			return spec.RegistryID
		}
	}
	return strings.ToLower(displayName)
}
