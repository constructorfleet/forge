package storage_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/storage"
)

// TestReaderHandle_ReadsWhileWriterWrites covers the dual-handle substrate a
// live reader needs (ADR 0030): a separate normal read-write handle used
// read-only must observe transcript events appended through the writer handle
// while that writer is mid-run — not stalled behind its single connection.
func TestReaderHandle_ReadsWhileWriterWrites(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "forge.db")

	writer, err := storage.Open(dsn)
	if err != nil {
		t.Fatalf("writer Open: %v", err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	ctx := context.Background()
	if err := writer.Migrate(ctx); err != nil {
		t.Fatalf("writer Migrate: %v", err)
	}

	seedIssueForAgentRun(t, writer, "exec-dual", "issue-dual")
	runID, err := writer.RecordAgentRun(ctx, storage.AgentRun{
		ExecutionID: "exec-dual", IssueID: "issue-dual",
		Backend: "claude-code", StartedAt: time.Now(), FinishedAt: time.Now(), Result: "IMPLEMENTED",
	})
	if err != nil {
		t.Fatalf("RecordAgentRun: %v", err)
	}

	// A separate handle on the same WAL database, used read-only.
	reader, err := storage.Open(dsn)
	if err != nil {
		t.Fatalf("reader Open: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })

	// Write through the writer handle, read through the reader handle.
	for i := 0; i < 20; i++ {
		if err := writer.RecordTranscriptEvents(ctx, "exec-dual", "issue-dual", runID, []storage.TranscriptEvent{
			{Seq: i, Type: "MESSAGE", Text: "during", OccurredAt: time.Now()},
		}); err != nil {
			t.Fatalf("writer RecordTranscriptEvents #%d: %v", i, err)
		}

		got, err := reader.TranscriptEventsAfter(ctx, runID, int64(i-1), 1)
		if err != nil {
			t.Fatalf("reader TranscriptEventsAfter #%d: %v", i, err)
		}
		if len(got) != 1 || got[0].Seq != i {
			t.Fatalf("reader got %+v for cursor %d, want the event just written", got, i-1)
		}
	}
}
