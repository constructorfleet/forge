package main

import "testing"

func TestValidateFeatureID(t *testing.T) {
	cases := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{name: "simple", id: "foo", wantErr: false},
		{name: "hyphen", id: "foo-bar", wantErr: false},
		{name: "underscore and digit", id: "feat_1", wantErr: false},
		{name: "empty", id: "", wantErr: true},
		{name: "parent traversal", id: "../evil", wantErr: true},
		{name: "nested separator", id: "a/b", wantErr: true},
		{name: "leading dot", id: ".hidden", wantErr: true},
		{name: "trailing separator", id: "foo/", wantErr: true},
		{name: "absolute path", id: "/etc/passwd", wantErr: true},
		{name: "backslash separator", id: "a\\b", wantErr: true},
		{name: "embedded dot-dot without separator", id: "foo..bar", wantErr: true},
		{name: "unsafe character", id: "foo bar", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateFeatureID(tc.id)
			if tc.wantErr && err == nil {
				t.Fatalf("validateFeatureID(%q) = nil, want error", tc.id)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateFeatureID(%q) = %v, want nil", tc.id, err)
			}
		})
	}
}
