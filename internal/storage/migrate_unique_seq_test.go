package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// TestMigration0027_UniquesStableSeq verifies the full table rebuild in
// 0027_transcript_events_unique_seq.sql: it preserves existing rows and adds
// a UNIQUE (agent_run_id, seq) constraint so a double-write of the same
// stable per-run ordinal is loud rather than silently duplicated (ADR 0030).
func TestMigration0027_UniquesStableSeq(t *testing.T) {
	const target = "0027_transcript_events_unique_seq.sql"

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
	exec(`INSERT INTO agent_runs (id, execution_id, issue_id, backend, started_at, finished_at, result, context_bytes, input_tokens, output_tokens)
		VALUES (7, 'exec-1', 'issue-1', 'claude-code', '2026-08-31T00:00:00Z', '2026-08-31T00:00:05Z', 'IMPLEMENTED', 2048, 11, 22)`)
	exec(`INSERT INTO transcript_events (id, execution_id, issue_id, agent_run_id, seq, type, role, text, occurred_at, tool_call_id, phase, subagent)
		VALUES (9, 'exec-1', 'issue-1', 7, 1, 'MESSAGE', 'assistant', 'hello', '2026-08-31T00:00:01Z', '', 'IMPLEMENTING', '')`)

	contents, err := migrationFiles.ReadFile("migrations/" + target)
	if err != nil {
		t.Fatalf("read migration %s: %v", target, err)
	}
	if err := applyMigration(ctx, db, target, string(contents)); err != nil {
		t.Fatalf("apply migration %s: %v", target, err)
	}

	var (
		eventID, agentRunID, seq int64
		text                     string
	)
	if err := db.QueryRowContext(ctx, `SELECT id, agent_run_id, seq, text FROM transcript_events`).Scan(&eventID, &agentRunID, &seq, &text); err != nil {
		t.Fatalf("read transcript_events after migration: %v", err)
	}
	if eventID != 9 || agentRunID != 7 || seq != 1 || text != "hello" {
		t.Fatalf("transcript_events row = (%d, %d, %d, %q), want (9, 7, 1, hello)", eventID, agentRunID, seq, text)
	}

	// Same run, same seq must now be rejected by the UNIQUE constraint.
	if _, err := db.ExecContext(ctx, `INSERT INTO transcript_events (execution_id, issue_id, agent_run_id, seq, type, occurred_at)
		VALUES ('exec-1', 'issue-1', 7, 1, 'MESSAGE', '2026-08-31T00:00:09Z')`); err == nil {
		t.Fatal("insert duplicate (agent_run_id, seq): want UNIQUE violation, got nil")
	}
}
