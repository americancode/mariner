package migrations

import (
	"database/sql"
	"testing"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

func TestUpCreatesAndRecordsBaseline(t *testing.T) {
	db, err := sql.Open("sqlite", "file:migrations-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := Up(db, "sqlite"); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	if err := Up(db, "sqlite"); err != nil {
		t.Fatalf("reapply migrations: %v", err)
	}

	var tables int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name IN ('vaults', 'audit_events', 'organizations', 'user_preferences')`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 4 {
		t.Fatalf("application tables = %d, want 4", tables)
	}

	var version int64
	var applied bool
	if err := db.QueryRow(`SELECT version_id, is_applied FROM goose_db_version WHERE version_id = 1`).Scan(&version, &applied); err != nil {
		t.Fatal(err)
	}
	if version != 1 || !applied {
		t.Fatalf("baseline migration record = version %d applied %v", version, applied)
	}
}

func TestDialectFor(t *testing.T) {
	for _, test := range []struct {
		driver  string
		dialect string
	}{
		{driver: "sqlite", dialect: "sqlite3"},
		{driver: "sqlite3", dialect: "sqlite3"},
		{driver: "postgres", dialect: "postgres"},
	} {
		dialect, err := dialectFor(test.driver)
		if err != nil || dialect != test.dialect {
			t.Fatalf("dialectFor(%q) = %q, %v; want %q", test.driver, dialect, err, test.dialect)
		}
	}
	if _, err := dialectFor("mysql"); err == nil {
		t.Fatal("dialectFor(mysql) unexpectedly succeeded")
	}
}

func TestNormalizeLegacyTimestamps(t *testing.T) {
	db, err := sql.Open("sqlite", "file:migrations-normalize-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	goose.SetBaseFS(Files)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(db, ".", 2); err != nil {
		t.Fatalf("apply migrations through version 2: %v", err)
	}
	legacy := "2026-10-19 15:30:00+00"
	for _, table := range []string{"vaults", "audit_events", "organizations", "user_preferences", "application_settings"} {
		var query string
		switch table {
		case "vaults":
			query = "INSERT INTO vaults(user_id, salt, nonce, ciphertext, updated_at) VALUES('user', 'salt', 'nonce', 'ciphertext', ?)"
		case "audit_events":
			query = "INSERT INTO audit_events(event_id, occurred_at, event_json) VALUES('event', ?, '{}')"
		case "organizations":
			query = "INSERT INTO organizations(id, encrypted, updated_at) VALUES('org', 'encrypted', ?)"
		case "user_preferences":
			query = "INSERT INTO user_preferences(user_id, settings_json, updated_at) VALUES('user', '{}', ?)"
		case "application_settings":
			query = "INSERT INTO application_settings(name, value, updated_at) VALUES('setting', 'value', ?)"
		}
		if _, err := db.Exec(query, legacy); err != nil {
			t.Fatal(err)
		}
	}
	if err := Up(db, "sqlite"); err != nil {
		t.Fatalf("apply normalization migration: %v", err)
	}
	for _, tableColumn := range []struct{ table, column string }{
		{table: "vaults", column: "updated_at"},
		{table: "audit_events", column: "occurred_at"},
		{table: "organizations", column: "updated_at"},
		{table: "user_preferences", column: "updated_at"},
		{table: "application_settings", column: "updated_at"},
	} {
		var value string
		if err := db.QueryRow("SELECT " + tableColumn.column + " FROM " + tableColumn.table + " LIMIT 1").Scan(&value); err != nil {
			t.Fatal(err)
		}
		if value != "2026-10-19T15:30:00Z" {
			t.Errorf("%s.%s = %q, want canonical UTC timestamp", tableColumn.table, tableColumn.column, value)
		}
	}
}
