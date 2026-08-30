package lspdriver

import "testing"

// TestLanguageIDFor pins the languageId the driver declares in
// textDocument/didOpen. It is per *file*, not per server: one driver
// (typescript-language-server) serves several of them, and a server handed
// the wrong languageId — every document declared "go", as this driver did
// while it was gopls-only — may ignore the document entirely.
func TestLanguageIDFor(t *testing.T) {
	cases := []struct {
		file string
		want string
	}{
		{"pkg/service.go", "go"},
		{"src/lib.rs", "rust"},
		{"app/service.py", "python"},
		{"app/service.pyi", "python"},
		{"web/app.js", "javascript"},
		{"web/app.mjs", "javascript"},
		{"web/app.cjs", "javascript"},
		{"web/app.jsx", "javascriptreact"},
		{"web/app.ts", "typescript"},
		{"web/app.mts", "typescript"},
		{"web/app.cts", "typescript"},
		{"web/app.tsx", "typescriptreact"},
		{"web/App.TSX", "typescriptreact"},
		// An extension with no known languageId falls back to the
		// extension itself, which is what most servers use anyway, rather
		// than mislabeling the document as some other language.
		{"deploy/main.tf", "tf"},
		{"Makefile", "plaintext"},
	}

	for _, tc := range cases {
		if got := languageIDFor(tc.file); got != tc.want {
			t.Errorf("languageIDFor(%q) = %q, want %q", tc.file, got, tc.want)
		}
	}
}
