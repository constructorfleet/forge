package storage_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Teagan42/forge/internal/storage"
)

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

func TestVerifySchemaRejectsUnmigratedStore(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open("file:verify-schema?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.VerifySchema(ctx); err == nil {
		t.Fatal("VerifySchema succeeded on an unmigrated store")
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.VerifySchema(ctx); err != nil {
		t.Fatalf("VerifySchema after Migrate: %v", err)
	}
}
