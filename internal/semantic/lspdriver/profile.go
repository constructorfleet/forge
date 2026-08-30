package lspdriver

// HoverStyle selects how a server's hover markdown is split into a
// signature and documentation. Servers disagree on this shape: gopls and
// typescript-language-server put the signature in the first fenced code
// block; rust-analyzer's first fence is the crate/module path with the
// signature in the second; pyright prefixes its signature with a
// "(function)"/"(class)" kind annotation and separates the docstring with
// a "---" horizontal rule.
type HoverStyle int

const (
	// HoverStyleFirstFence treats the first fenced code block as the
	// signature and everything else as documentation. This is the zero
	// value — the safe generic default (go, typescript).
	HoverStyleFirstFence HoverStyle = iota

	// HoverStyleRustTwoFence treats the second fenced code block as the
	// signature, since rust-analyzer's first fence is the crate/module
	// path rather than the signature.
	HoverStyleRustTwoFence

	// HoverStylePyrightAnnotated strips a leading "(function)"/"(class)"
	// kind annotation from the signature and cuts documentation at the
	// "---" horizontal rule pyright emits before the docstring.
	HoverStylePyrightAnnotated
)

// ServerProfile carries a language server's declarative per-server
// behavior: the initializationOptions to send during the handshake, how to
// split its hover markdown, and whether to drop child symbols from
// documentSymbol results. It is plain data — no function pointers — so a
// new language server is supported by adding a registry row, not a new
// driver type (see ADR 0015). The zero value is the safe generic default:
// no init options, HoverStyleFirstFence, no dropping.
type ServerProfile struct {
	// InitOptions is sent as initializationOptions during the initialize
	// handshake. Nil means no initializationOptions are sent.
	InitOptions map[string]any

	// HoverStyle selects how hover markdown is split into a signature and
	// documentation.
	HoverStyle HoverStyle

	// DropSymbolChildren, when true, excludes child symbols (e.g.
	// pyright's function parameters) from DocumentSymbols results.
	DropSymbolChildren bool
}
