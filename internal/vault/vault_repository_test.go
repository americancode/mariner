package vault

import (
	"testing"

	"mariner/internal/model"
	_ "modernc.org/sqlite"
)

func TestVaultTableRepositoryPreservesEncryptedVaultBehavior(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	data := model.Data{Connections: []model.Connection{{ID: "connection-1", Name: "Demo", Bucket: "bucket"}}}
	if err := store.VaultRepository().Save("user-1", "correct horse battery", data); err != nil {
		t.Fatalf("save vault: %v", err)
	}
	loaded, exists, err := store.VaultRepository().Load("user-1", "correct horse battery")
	if err != nil {
		t.Fatalf("load vault: %v", err)
	}
	if !exists || len(loaded.Connections) != 1 || loaded.Connections[0].Bucket != "bucket" {
		t.Fatalf("loaded=%+v exists=%v, want saved vault", loaded, exists)
	}
	if _, _, err := store.VaultRepository().Load("user-1", "wrong password"); err == nil {
		t.Fatal("wrong password unexpectedly decrypted vault")
	}
}
