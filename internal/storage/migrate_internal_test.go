package storage

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"sort"
	"testing"
)

// TestMigration0020PreservesExistingRows is a white-box test of the
// create-copy-drop-rename rebuild in 0020_planning_scoped_agent_runs.sql.
// Migrating a *fresh* database exercises none of it: every table is empty,
// so the copies move no rows and DROP TABLE's FK-checked implicit DELETE has
// nothing to trip over. This applies every migration up to 0019, seeds a
// realistic Phase-1 execution (an execution_issues row with an agent_runs
// row, its transcript_events, and events), then applies 0020 alone and
// asserts the rows -- ids included -- survive it.
func TestMigration0020PreservesExistingRows(t *testing.T) {
	const target = "0020_planning_scoped_agent_runs.sql"

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
	exec(`INSERT INTO events (id, execution_id, issue_id, type, data, occurred_at)
		VALUES (5, 'exec-1', 'issue-1', 'agent.run', '{}', '2026-08-31T00:00:05Z')`)

	contents, err := migrationFiles.ReadFile("migrations/" + target)
	if err != nil {
		t.Fatalf("read migration %s: %v", target, err)
	}
	if err := applyMigration(ctx, db, target, string(contents)); err != nil {
		t.Fatalf("apply migration %s: %v", target, err)
	}

	var (
		runID, runTokens int64
		backend, result  string
	)
	if err := db.QueryRowContext(ctx, `SELECT id, backend, result, input_tokens FROM agent_runs`).Scan(&runID, &backend, &result, &runTokens); err != nil {
		t.Fatalf("read agent_runs after 0020: %v", err)
	}
	if runID != 7 || backend != "claude-code" || result != "IMPLEMENTED" || runTokens != 11 {
		t.Fatalf("agent_runs row = (%d, %q, %q, %d), want (7, claude-code, IMPLEMENTED, 11)", runID, backend, result, runTokens)
	}

	var (
		eventID, agentRunID int64
		text, phase         string
	)
	if err := db.QueryRowContext(ctx, `SELECT id, agent_run_id, text, phase FROM transcript_events`).Scan(&eventID, &agentRunID, &text, &phase); err != nil {
		t.Fatalf("read transcript_events after 0020: %v", err)
	}
	if eventID != 9 || agentRunID != 7 || text != "hello" || phase != "IMPLEMENTING" {
		t.Fatalf("transcript_events row = (%d, %d, %q, %q), want (9, 7, hello, IMPLEMENTING)", eventID, agentRunID, text, phase)
	}

	var (
		logID    int64
		logType  string
		issueRef string
	)
	if err := db.QueryRowContext(ctx, `SELECT id, type, issue_id FROM events`).Scan(&logID, &logType, &issueRef); err != nil {
		t.Fatalf("read events after 0020: %v", err)
	}
	if logID != 5 || logType != "agent.run" || issueRef != "issue-1" {
		t.Fatalf("events row = (%d, %q, %q), want (5, agent.run, issue-1)", logID, logType, issueRef)
	}

	// The rebuilt transcript_events must still enforce its FK to the
	// renamed agent_runs table, not to the intermediate agent_runs_new.
	if _, err := db.ExecContext(ctx, `INSERT INTO transcript_events (execution_id, issue_id, agent_run_id, seq, type, occurred_at)
		VALUES ('exec-1', 'issue-1', 4242, 1, 'MESSAGE', '2026-08-31T00:00:09Z')`); err == nil {
		t.Fatal("insert transcript event for unknown agent_run_id: want FK error, got nil")
	}
}

func migrationNames(t *testing.T) []string {
	t.Helper()
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
}
