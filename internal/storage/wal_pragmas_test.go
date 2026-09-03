package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestWithPragmas_IncludesWALAndSynchronousNormal(t *testing.T) {
	dsn := withPragmas(filepath.Join(t.TempDir(), "forge.db"))

	if !strings.Contains(dsn, "_pragma=journal_mode(WAL)") {
		t.Fatalf("dsn %q missing _pragma=journal_mode(WAL)", dsn)
	}
	if !strings.Contains(dsn, "_pragma=synchronous(NORMAL)") {
		t.Fatalf("dsn %q missing _pragma=synchronous(NORMAL)", dsn)
	}
	if !strings.Contains(dsn, "_pragma=foreign_keys(1)") {
		t.Fatalf("dsn %q missing _pragma=foreign_keys(1)", dsn)
	}
	if !strings.Contains(dsn, "_pragma=busy_timeout(5000)") {
		t.Fatalf("dsn %q missing _pragma=busy_timeout(5000)", dsn)
	}
}

func TestWithPragmas_AppendsToExistingQueryString(t *testing.T) {
	dsn := withPragmas("file:db?cache=shared")
	if !strings.Contains(dsn, "cache=shared&_pragma=") {
		t.Fatalf("dsn %q: pragmas not appended with & to existing query", dsn)
	}
}

func TestOpen_FileDBUsesWALMode(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "forge.db")
	store, err := Open(dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	var mode string
	if err := store.db.QueryRowContext(context.Background(), `PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}

	var sync string
	if err := store.db.QueryRowContext(context.Background(), `PRAGMA synchronous`).Scan(&sync); err != nil {
		t.Fatalf("PRAGMA synchronous: %v", err)
	}
	// synchronous(NORMAL) reads back as 1.
	if sync != "1" && sync != "normal" {
		t.Fatalf("synchronous = %q, want normal/1", sync)
	}
}

func TestOpen_MemoryDBWALIsNoop(t *testing.T) {
	store, err := Open("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	var mode sql.NullString
	if err := store.db.QueryRowContext(context.Background(), `PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	// WAL is a no-op for in-memory DBs; the driver reports memory/delete.
	if mode.Valid && mode.String == "wal" {
		t.Fatalf("journal_mode = wal for an in-memory DB, want non-WAL")
	}
}
