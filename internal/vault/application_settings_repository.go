package vault

import (
	"database/sql"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	"mariner/internal/model"
)

type ApplicationSettingsTableRepository struct {
	db *sqlx.DB
	mu *sync.Mutex
}

func newApplicationSettingsTableRepository(db *sqlx.DB, mu *sync.Mutex) *ApplicationSettingsTableRepository {
	return &ApplicationSettingsTableRepository{db: db, mu: mu}
}

func (r *ApplicationSettingsTableRepository) Get() (model.ApplicationSettings, error) {
	var raw string
	err := r.db.Get(&raw, r.db.Rebind("SELECT value FROM application_settings WHERE name = ?"), "archive_max_bytes")
	if errors.Is(err, sql.ErrNoRows) {
		return model.ApplicationSettings{ArchiveMaxBytes: model.DefaultArchiveMaxBytes}, nil
	}
	if err != nil {
		return model.ApplicationSettings{}, err
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return model.ApplicationSettings{}, errors.New("invalid archive max bytes setting")
	}
	return model.ApplicationSettings{ArchiveMaxBytes: value}, nil
}

func (r *ApplicationSettingsTableRepository) Save(settings model.ApplicationSettings) error {
	if settings.ArchiveMaxBytes <= 0 {
		return errors.New("archive max bytes must be positive")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := r.db.Exec(r.db.Rebind(`INSERT INTO application_settings(name, value, updated_at) VALUES(?,?,?) ON CONFLICT(name) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`), "archive_max_bytes", strconv.FormatInt(settings.ArchiveMaxBytes, 10), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
