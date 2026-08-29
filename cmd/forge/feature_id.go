package main

import (
	"fmt"
	"strings"
)

// validateFeatureID rejects Feature ids that are unsafe to use as a
// directory name under .forge/features/. It performs no filesystem access.
func validateFeatureID(id string) error {
	if id == "" {
		return fmt.Errorf("feature id %q is invalid: must not be empty", id)
	}
	if strings.Contains(id, "/") || strings.Contains(id, "\\") {
		return fmt.Errorf("feature id %q is invalid: must not contain path separators", id)
	}
	if strings.Contains(id, "..") {
		return fmt.Errorf("feature id %q is invalid: must not contain \"..\"", id)
	}
	if strings.HasPrefix(id, ".") {
		return fmt.Errorf("feature id %q is invalid: must not start with \".\"", id)
	}
	for _, r := range id {
		if !isSafeFeatureIDRune(r) {
			return fmt.Errorf("feature id %q is invalid: character %q is not allowed (allowed: A-Z a-z 0-9 . _ -)", id, r)
		}
	}
	return nil
}

func isSafeFeatureIDRune(r rune) bool {
	switch {
	case r >= 'A' && r <= 'Z':
		return true
	case r >= 'a' && r <= 'z':
		return true
	case r >= '0' && r <= '9':
		return true
	case r == '.' || r == '_' || r == '-':
		return true
	default:
		return false
	}
}
