package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// TestMigration0031_DropsWeakWorkersUnique verifies the full table rebuild
// in 0031_workers_drop_weak_unique.sql: it preserves existing worker rows
// and drops the table-level UNIQUE(execution_id, issue_id) constraint from
// 0001_init.sql, leaving idx_workers_issue_claim_unique (0011) as the only
// constraint enforcing one active claim per Issue.
func TestMigration0031_DropsWeakWorkersUnique(t *testing.T) {
	const target = "0031_workers_drop_weak_unique.sql"

	ctx := context.Background()
	dsn := withPragmas(filepath.Join(t.TempDir(), "forge.db"))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	for _, name := range migrationNames(t) {
		if name >= target {
			continue
		}
		contents, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		if err := applyMigration(ctx, db, name, string(contents)); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}

	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("seed %q: %v", query, err)
		}
	}
	exec(`INSERT INTO executions (id, base_revision, started_at) VALUES ('exec-1', 'abc123', '2026-08-31T00:00:00Z')`)
	exec(`INSERT INTO execution_issues
		(execution_id, issue_id, state, scope, retry_gate_limit, retry_gate_used, retry_review_limit, retry_review_used, retry_ci_limit, retry_ci_used)
		VALUES ('exec-1', 'issue-1', 'IMPLEMENTING', 'MANAGED', 3, 0, 3, 0, 3, 0)`)
	exec(`INSERT INTO workers (id, execution_id, issue_id, worker_ref, claimed_at, owner_pid, last_heartbeat, owner_token)
		VALUES (3, 'exec-1', 'issue-1', 'worker-a', '2026-08-31T00:00:00Z', 42, '2026-08-31T00:00:01Z', 'token-a')`)

	contents, err := migrationFiles.ReadFile("migrations/" + target)
	if err != nil {
		t.Fatalf("read migration %s: %v", target, err)
	}
	if err := applyMigration(ctx, db, target, string(contents)); err != nil {
		t.Fatalf("apply migration %s: %v", target, err)
	}

	var (
		id                    int64
		executionID, issueID  string
		workerRef, ownerToken string
		ownerPID              int64
	)
	if err := db.QueryRowContext(ctx, `SELECT id, execution_id, issue_id, worker_ref, owner_pid, owner_token FROM workers`).
		Scan(&id, &executionID, &issueID, &workerRef, &ownerPID, &ownerToken); err != nil {
		t.Fatalf("read workers after 0031: %v", err)
	}
	if id != 3 || executionID != "exec-1" || issueID != "issue-1" || workerRef != "worker-a" || ownerPID != 42 || ownerToken != "token-a" {
		t.Fatalf("workers row = (%d, %q, %q, %q, %d, %q), want (3, exec-1, issue-1, worker-a, 42, token-a)",
			id, executionID, issueID, workerRef, ownerPID, ownerToken)
	}

	// idx_workers_issue_claim_unique (0011) must still reject a second
	// claim on the same issue_id.
	if _, err := db.ExecContext(ctx, `INSERT INTO workers (execution_id, issue_id, worker_ref, claimed_at)
		VALUES ('exec-1', 'issue-1', 'worker-b', '2026-08-31T00:00:02Z')`); err == nil {
		t.Fatal("insert second claim for same issue_id: want UNIQUE violation, got nil")
	}

	// The table-level UNIQUE(execution_id, issue_id) from 0001_init.sql
	// must be gone: idx_workers_issue_claim_unique is the only unique
	// index left on workers.
	rows, err := db.QueryContext(ctx, `PRAGMA index_list(workers)`)
	if err != nil {
		t.Fatalf("PRAGMA index_list(workers): %v", err)
	}
	defer rows.Close()

	var uniqueIndexes []string
	for rows.Next() {
		var (
			seq, unique, partial int
			name, origin         string
		)
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			t.Fatalf("scan index_list row: %v", err)
		}
		if unique == 1 {
			uniqueIndexes = append(uniqueIndexes, name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate index_list: %v", err)
	}
	if len(uniqueIndexes) != 1 || uniqueIndexes[0] != "idx_workers_issue_claim_unique" {
		t.Fatalf("unique indexes on workers = %v, want [idx_workers_issue_claim_unique]", uniqueIndexes)
	}
}
