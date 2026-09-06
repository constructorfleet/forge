package lsp

import "os/exec"

// ProbeBinaries checks spec's candidate binaries against PATH in the order
// the Language-to-LSP Table lists them, and returns the first one
// exec.LookPath resolves. It probes only: it never prompts the user,
// invokes a package manager, or downloads anything. When no candidate
// resolves, it returns ("", false) and the caller must treat the language
// as missing its Language Server.
func ProbeBinaries(spec LanguageSpec) (string, bool) {
	for _, candidate := range spec.Binaries {
		if _, err := exec.LookPath(candidate.Name); err == nil {
			return candidate.Name, true
		}
	}
	return "", false
}
