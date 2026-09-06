package lsp

// BinaryCandidate is one Language Server binary Forge may run for a
// language, in the order Forge tries it, with the static install hint to
// show when the binary is not on PATH.
type BinaryCandidate struct {
	Name        string
	InstallHint string
}

// LanguageSpec is one row of the Language-to-LSP Table: a language, the
// manifest filenames and fallback file extensions that detect it (see
// Scan), and the ordered candidate Language Server binaries Forge tries
// for it.
type LanguageSpec struct {
	Language           string
	ManifestFilenames  []string
	FallbackExtensions []string
	Binaries           []BinaryCandidate
}

// Languages is the static, ordered Language-to-LSP Table: the sole source
// of truth for which languages Forge supports, the ordered candidate
// binaries it tries for each one, and each binary's install hint. No
// configuration file, environment variable, or flag overrides this order
// or pins a binary.
//
// Wiring a real caller — Scan's manifest/extension inputs, or
// initdiscovery's hand-rolled language table — to read from Languages
// instead of a second hand-written table is deferred to a follow-up
// ticket.
//
// detect.go's Registry (seeded from builtinServers) is a separate,
// already-wired table that governs which Language Server actually starts
// for a detected language, keyed by lowercase language identifier
// ("go", "python") rather than this table's display names, and it can
// name different commands (for example pyright-langserver --stdio for
// Python, versus this table's bare pyright). Reconciling Languages with
// Registry — so this table drives Detect's server selection too — is
// deferred to a follow-up ticket; until then the two tables coexist and a
// caller must not assume they agree.
var Languages = []LanguageSpec{
	{
		Language:           "Go",
		ManifestFilenames:  []string{"go.mod"},
		FallbackExtensions: []string{".go"},
		Binaries: []BinaryCandidate{
			{Name: "gopls", InstallHint: "go install golang.org/x/tools/gopls@latest"},
		},
	},
	{
		Language:           "TypeScript/JavaScript",
		ManifestFilenames:  []string{"package.json"},
		FallbackExtensions: []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"},
		Binaries: []BinaryCandidate{
			{Name: "typescript-language-server", InstallHint: "npm install -g typescript-language-server typescript"},
		},
	},
	{
		Language:           "Python",
		ManifestFilenames:  []string{"pyproject.toml", "requirements.txt", "setup.py"},
		FallbackExtensions: []string{".py"},
		Binaries: []BinaryCandidate{
			{Name: "pyright", InstallHint: "npm install -g pyright"},
			{Name: "pylsp", InstallHint: "pip install python-lsp-server"},
		},
	},
	{
		Language:           "Rust",
		ManifestFilenames:  []string{"Cargo.toml"},
		FallbackExtensions: []string{".rs"},
		Binaries: []BinaryCandidate{
			{Name: "rust-analyzer", InstallHint: "rustup component add rust-analyzer"},
		},
	},
	{
		Language:           "C/C++",
		ManifestFilenames:  []string{"CMakeLists.txt", "Makefile"},
		FallbackExtensions: []string{".c", ".h", ".cpp", ".cc", ".cxx", ".hpp"},
		Binaries: []BinaryCandidate{
			{Name: "clangd", InstallHint: "brew install llvm"},
		},
	},
	{
		Language:           "Java",
		ManifestFilenames:  []string{"pom.xml", "build.gradle", "build.gradle.kts"},
		FallbackExtensions: []string{".java"},
		Binaries: []BinaryCandidate{
			{Name: "jdtls", InstallHint: "brew install jdtls"},
		},
	},
	{
		Language:           "Ruby",
		ManifestFilenames:  []string{"Gemfile"},
		FallbackExtensions: []string{".rb"},
		Binaries: []BinaryCandidate{
			{Name: "solargraph", InstallHint: "gem install solargraph"},
			{Name: "ruby-lsp", InstallHint: "gem install ruby-lsp"},
		},
	},
}
