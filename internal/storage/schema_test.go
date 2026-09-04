package storage_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Teagan42/forge/internal/storage"
)

// TestLivenessColumnsPresentAfterMigrate proves a migrated store exposes the
// liveness columns the roster renders from.
func TestLivenessColumnsPresentAfterMigrate(t *testing.T) {
	store := openTestStore(t)
	ok, err := store.LivenessColumnsPresent(context.Background())
	if err != nil {
		t.Fatalf("LivenessColumnsPresent: %v", err)
	}
	if !ok {
		t.Fatal("LivenessColumnsPresent = false after Migrate, want true")
	}
}

// TestLivenessColumnsAbsentBeforeMigrate proves an unmigrated, freshly-opened
// store (the read-only watch path, which never runs Migrate) reports the
// columns absent, so watch fails loudly against a pre-0028 database.
func TestLivenessColumnsAbsentBeforeMigrate(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "forge.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	ok, err := store.LivenessColumnsPresent(context.Background())
	if err != nil {
		t.Fatalf("LivenessColumnsPresent: %v", err)
	}
	if ok {
		t.Fatal("LivenessColumnsPresent = true before Migrate, want false")
	}
}
