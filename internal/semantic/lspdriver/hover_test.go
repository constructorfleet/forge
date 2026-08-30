package lspdriver

import "testing"

func TestSplitHoverMarkdown(t *testing.T) {
	tests := []struct {
		name          string
		style         HoverStyle
		value         string
		wantSignature string
		wantDoc       string
	}{
		{
			name:          "firstFence takes the only fence as the signature",
			style:         HoverStyleFirstFence,
			value:         "```go\nfunc Greet(name string) string\n```\n\nGreet returns an English greeting for name.",
			wantSignature: "func Greet(name string) string",
			wantDoc:       "Greet returns an English greeting for name.",
		},
		{
			name:          "rustTwoFence skips the crate-path fence and takes the second",
			style:         HoverStyleRustTwoFence,
			value:         "```rust\nmycrate::MyStruct\n```\n\n```rust\npub fn foo(&self, x: i32) -> String\n```\n\nDoc comment for foo.",
			wantSignature: "pub fn foo(&self, x: i32) -> String",
			wantDoc:       "mycrate::MyStruct\nDoc comment for foo.",
		},
		{
			name:          "pyrightAnnotated strips the (function) prefix and cuts at the HR",
			style:         HoverStylePyrightAnnotated,
			value:         "```python\n(function) def foo(x: int) -> str\n```\n---\nDocstring here.",
			wantSignature: "def foo(x: int) -> str",
			wantDoc:       "Docstring here.",
		},
		{
			name:          "pyrightAnnotated strips (class) prefix too",
			style:         HoverStylePyrightAnnotated,
			value:         "```python\n(class) Foo\n```\n---\nA class.",
			wantSignature: "Foo",
			wantDoc:       "A class.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSig, gotDoc := splitHoverMarkdown(tt.style, tt.value)
			if gotSig != tt.wantSignature {
				t.Errorf("signature = %q, want %q", gotSig, tt.wantSignature)
			}
			if gotDoc != tt.wantDoc {
				t.Errorf("documentation = %q, want %q", gotDoc, tt.wantDoc)
			}
		})
	}
}
