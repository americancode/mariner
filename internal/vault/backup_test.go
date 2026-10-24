package vault

import (
	"testing"

	"mariner/internal/model"
	_ "modernc.org/sqlite"
)

func TestExportImportBundleRoundTripPreservesEncryptedData(t *testing.T) {
	source, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer source.Close()
	if err := source.VaultRepository().Save("user-1", "vault-password", model.Data{Connections: []model.Connection{{ID: "c1", Name: "Demo", Bucket: "bucket"}}}); err != nil {
		t.Fatalf("save vault: %v", err)
	}
	if err := source.UserPreferenceRepository().Save("user-1", map[string]bool{"compact": true}); err != nil {
		t.Fatalf("save preferences: %v", err)
	}
	if _, err := source.AuditRepository().Append(model.AuditEvent{ID: "event-1", OccurredAt: "2026-01-01T00:00:00Z", Action: "test", Result: "success"}); err != nil {
		t.Fatalf("save audit event: %v", err)
	}
	bundle, err := source.ExportBundle()
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if bundle.Version != ExportBundleVersion || len(bundle.Vaults) != 1 || len(bundle.Preferences) != 1 || len(bundle.AuditEvents) != 1 {
		t.Fatalf("bundle = %+v", bundle)
	}

	destination, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open destination: %v", err)
	}
	defer destination.Close()
	if err := destination.ImportBundle(bundle); err != nil {
		t.Fatalf("import: %v", err)
	}
	loaded, exists, err := destination.VaultRepository().Load("user-1", "vault-password")
	if err != nil || !exists || len(loaded.Connections) != 1 {
		t.Fatalf("loaded=%+v exists=%v err=%v", loaded, exists, err)
	}
	preferences, err := destination.UserPreferenceRepository().Get("user-1")
	if err != nil || !preferences["compact"] {
		t.Fatalf("preferences=%v err=%v", preferences, err)
	}
	if _, err := destination.AuditRepository().ListAfter("", "", 10); err != nil {
		t.Fatalf("audit data was not imported: %v", err)
	}
}

func TestImportBundleRejectsUnsupportedVersionBeforeReplacingData(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := store.VaultRepository().Save("user-1", "password", model.Data{}); err != nil {
		t.Fatalf("save vault: %v", err)
	}
	err = store.ImportBundle(ExportBundle{Format: "mariner-export", Version: ExportBundleVersion + 1, ExportedAt: "2026-01-01T00:00:00Z"})
	if err == nil {
		t.Fatal("unsupported bundle version imported")
	}
	if _, exists, loadErr := store.VaultRepository().Load("user-1", "password"); loadErr != nil || !exists {
		t.Fatalf("existing data changed: exists=%v err=%v", exists, loadErr)
	}
}
