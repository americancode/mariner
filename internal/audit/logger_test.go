package audit

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"periscope/internal/vault"
	_ "modernc.org/sqlite"
)

func TestWritePersistsCanonicalJSONToDatabase(t *testing.T) {
	dir := t.TempDir()
	store, err := vault.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	logger := New(store)
	event := vault.AuditEvent{ID: "event-1", OccurredAt: "2026-08-31T18:00:00Z", Action: "connection.create", Result: "success", UserID: "user-1"}
	if err = logger.Write(event); err != nil {
		t.Fatal(err)
	}
	if err = logger.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", filepath.Join(dir, "periscope.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var databaseJSON string
	if err = db.QueryRow("SELECT event_json FROM audit_events WHERE event_id = ?", "event-1").Scan(&databaseJSON); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err = json.Unmarshal([]byte(databaseJSON), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["action"] != "connection.create" {
		t.Fatalf("unexpected event: %#v", decoded)
	}
}

func TestListAuditFiltersAndPaginatesInDatabase(t *testing.T) {
	store, err := vault.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	events := []vault.AuditEvent{
		{ID: "event-1", OccurredAt: "2026-08-31T18:00:00Z", Action: "object.upload", Result: "success", UserID: "user-1", Bucket: "bucket-a"},
		{ID: "event-2", OccurredAt: "2026-08-31T18:00:00Z", Action: "object.delete", Result: "success", UserID: "user-1", Bucket: "bucket-a"},
		{ID: "event-3", OccurredAt: "2026-08-30T18:00:00Z", Action: "object.upload", Result: "success", UserID: "user-1", Bucket: "bucket-a"},
	}
	for _, event := range events {
		if _, err = store.AppendAudit(event); err != nil {
			t.Fatal(err)
		}
	}

	page, err := store.ListAudit(vault.AuditFilter{
		User:   "user-1",
		Bucket: "bucket-a",
		Action: "object.upload",
		Start:  "2026-08-30T00:00:00Z",
		End:    "2026-08-31T23:59:59Z",
		Limit:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || string(page.Events[0]) == "" || !page.HasMore {
		t.Fatalf("unexpected filtered first page: events=%d hasMore=%v", len(page.Events), page.HasMore)
	}

	page, err = store.ListAudit(vault.AuditFilter{
		User:   "user-1",
		Bucket: "bucket-a",
		Action: "object.upload",
		Start:  "2026-08-30T00:00:00Z",
		End:    "2026-08-31T23:59:59Z",
		Limit:  1,
		Offset: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || page.HasMore {
		t.Fatalf("unexpected filtered second page: events=%d hasMore=%v", len(page.Events), page.HasMore)
	}
}
