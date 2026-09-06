package lsp

import (
	"strings"

	"github.com/Teagan42/forge/internal/semantic/lspdriver"
)

// BinaryCandidate is one Language Server binary Forge may run for a
// language, in the order Forge tries it: the binary name ProbeBinaries
// checks against PATH, the static install hint to show when it is absent,
// the argv that launches it, and the ServerProfile its quirks require.
type BinaryCandidate struct {
	Name        string
	InstallHint string
	// Command is the argv NewRegistry stores for this candidate when
	// ProbeBinaries selects it. An empty Command defaults to a single
	// argument: Name with no flags.
	Command []string
	// Profile is the ServerProfile (see lspdriver.ServerProfile) this
	// candidate's Language Server needs. The zero value fits a server with
	// no known hover or symbol quirk.
	Profile lspdriver.ServerProfile
}

// LanguageSpec is one row of the Language-to-LSP Table: a language, the
// RegistryID that names it in the Language Server Registry (Registry,
// NewRegistry, Detect), the manifest filenames and fallback file
// extensions that detect it (see Scan), and the ordered candidate
// Language Server binaries Forge tries for it.
type LanguageSpec struct {
	Language           string
	RegistryID         string
	ManifestFilenames  []string
	FallbackExtensions []string
	Binaries           []BinaryCandidate
}

// Languages is the static, ordered Language-to-LSP Table: the sole source
// of truth for which languages Forge supports, the RegistryID each one
// shares with lsp.Registry, the ordered candidate binaries it tries for
// each one, and each binary's launch command, ServerProfile, and static
// install hint. No configuration file, environment variable, or flag
// overrides this order or pins a binary.
//
// NewRegistry builds lsp.Registry from this table: for each language it
// runs ProbeBinaries and stores the first candidate ProbeBinaries finds on
// PATH, falling back to the first candidate when none resolves. This is
// the single source of truth for both PATH-availability reporting (see
// ProbeBinaries) and the command Detect actually launches.
var Languages = []LanguageSpec{
	{
		Language:           "Go",
		RegistryID:         "go",
		ManifestFilenames:  []string{"go.mod"},
		FallbackExtensions: []string{".go"},
		Binaries: []BinaryCandidate{
			{
				Name:        "gopls",
				InstallHint: "go install golang.org/x/tools/gopls@latest",
				Profile:     lspdriver.ServerProfile{HoverStyle: lspdriver.HoverStyleFirstFence},
			},
		},
	},
	{
		Language:           "TypeScript/JavaScript",
		RegistryID:         "javascript",
		ManifestFilenames:  []string{"package.json"},
		FallbackExtensions: []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"},
		Binaries: []BinaryCandidate{
			{
				Name:        "typescript-language-server",
				InstallHint: "npm install -g typescript-language-server typescript",
				Command:     []string{"typescript-language-server", "--stdio"},
				Profile:     lspdriver.ServerProfile{HoverStyle: lspdriver.HoverStyleFirstFence},
			},
		},
	},
	{
		Language:           "Python",
		RegistryID:         "python",
		ManifestFilenames:  []string{"pyproject.toml", "requirements.txt", "setup.py"},
		FallbackExtensions: []string{".py"},
		Binaries: []BinaryCandidate{
			{
				Name:        "pyright",
				InstallHint: "npm install -g pyright",
				Command:     []string{"pyright-langserver", "--stdio"},
				Profile: lspdriver.ServerProfile{
					HoverStyle:         lspdriver.HoverStylePyrightAnnotated,
					DropSymbolChildren: true,
				},
			},
			{Name: "pylsp", InstallHint: "pip install python-lsp-server"},
		},
	},
	{
		Language:           "Rust",
		RegistryID:         "rust",
		ManifestFilenames:  []string{"Cargo.toml"},
		FallbackExtensions: []string{".rs"},
		Binaries: []BinaryCandidate{
			{
				Name:        "rust-analyzer",
				InstallHint: "rustup component add rust-analyzer",
				Profile:     lspdriver.ServerProfile{HoverStyle: lspdriver.HoverStyleRustTwoFence},
			},
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
