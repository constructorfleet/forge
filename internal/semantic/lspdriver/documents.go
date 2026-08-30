package lspdriver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// ErrInert is returned by every query method when the Driver has no live
// connection (never started, still starting, or gone inert per the Driver
// doc comment).
var ErrInert = errors.New("gopls: driver has no live connection")

// readyServer returns the Driver's current live server, or ErrInert if
// there isn't one.
func (d *Driver) readyServer() (protocol.Server, error) {
	d.mu.Lock()
	server := d.server
	d.mu.Unlock()
	if server == nil {
		return nil, ErrInert
	}
	return server, nil
}

// languageIDs maps a file extension to the LSP languageId a
// textDocument/didOpen declares for it. This is the LSP's own vocabulary,
// not the Language Server Registry's: one registry language ("javascript")
// covers several languageIds, since typescript-language-server needs
// ".tsx" declared "typescriptreact" to serve it correctly.
var languageIDs = map[string]string{
	".go":   "go",
	".rs":   "rust",
	".py":   "python",
	".pyi":  "python",
	".js":   "javascript",
	".mjs":  "javascript",
	".cjs":  "javascript",
	".jsx":  "javascriptreact",
	".ts":   "typescript",
	".mts":  "typescript",
	".cts":  "typescript",
	".tsx":  "typescriptreact",
	".json": "json",
}

// languageIDFor returns the LSP languageId for file. An extension with no
// known languageId falls back to the extension itself (which is what most
// servers use for the long tail), and an extensionless file to
// "plaintext" — never to another language's identifier, which a server
// might act on.
func languageIDFor(file string) string {
	ext := strings.ToLower(filepath.Ext(file))
	if id, ok := languageIDs[ext]; ok {
		return id
	}
	if ext == "" {
		return "plaintext"
	}
	return strings.TrimPrefix(ext, ".")
}

// ensureOpen sends textDocument/didOpen for file the first time it's
// referenced by any query method on this connection, and is a no-op on
// every subsequent reference — the lazy-open behavior in the issue's
// acceptance criteria. openedMu is held for the whole check-and-send so a
// file is opened at most once even if two callers reference it
// concurrently.
func (d *Driver) ensureOpen(ctx context.Context, server protocol.Server, file string) (uri.URI, error) {
	abs, err := filepath.Abs(file)
	if err != nil {
		return "", err
	}
	docURI := uri.File(abs)

	d.openedMu.Lock()
	defer d.openedMu.Unlock()

	if _, ok := d.opened[abs]; ok {
		return docURI, nil
	}

	text, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}

	if err := server.DidOpen(ctx, &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        docURI,
			LanguageID: protocol.LanguageKind(languageIDFor(abs)),
			Version:    1,
			Text:       string(text),
		},
	}); err != nil {
		return "", err
	}

	if d.opened == nil {
		d.opened = make(map[string]struct{})
	}
	d.opened[abs] = struct{}{}

	return docURI, nil
}
