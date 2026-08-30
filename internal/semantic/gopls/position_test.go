package gopls

import (
	"testing"

	"go.lsp.dev/protocol"
)

func TestToLSPPosition(t *testing.T) {
	got := toLSPPosition(Position{Line: 10, Column: 9})
	want := protocol.Position{Line: 9, Character: 8}
	if got != want {
		t.Fatalf("toLSPPosition(Position{10, 9}) = %#v, want %#v", got, want)
	}
}

func TestFromLSPPosition(t *testing.T) {
	got := fromLSPPosition(protocol.Position{Line: 3, Character: 5})
	want := Position{Line: 4, Column: 6}
	if got != want {
		t.Fatalf("fromLSPPosition({3, 5}) = %#v, want %#v", got, want)
	}
}

func TestPositionRoundTrip(t *testing.T) {
	original := Position{Line: 42, Column: 17}
	got := fromLSPPosition(toLSPPosition(original))
	if got != original {
		t.Fatalf("round trip = %#v, want %#v", got, original)
	}
}
