package migrations

import (
	"database/sql"
	"embed"
	"fmt"
	"log/slog"

	"github.com/pressly/goose/v3"
)

// Files contains the checked-in schema migrations shipped with Mariner.
// Embedding keeps the application binary self-contained for SQLite and for
// deployments where a separate migration volume is not available.
//
//go:embed *.sql
var Files embed.FS

func Up(db *sql.DB, driver string) error {
	slog.Info("database migrations starting", "driver", driver)
	dialect, err := dialectFor(driver)
	if err != nil {
		return err
	}
	goose.SetBaseFS(Files)
	if err := goose.SetDialect(dialect); err != nil {
		return err
	}
	err = goose.Up(db, ".")
	if err != nil {
		slog.Error("database migrations failed", "driver", driver, "error", err)
		return err
	}
	slog.Info("database migrations complete", "driver", driver)
	return nil
}

func dialectFor(driver string) (string, error) {
	switch driver {
	case "sqlite", "sqlite3":
		return "sqlite3", nil
	case "postgres", "pgx":
		return "postgres", nil
	default:
		return "", fmt.Errorf("unsupported migration driver: %s", driver)
	}
}
