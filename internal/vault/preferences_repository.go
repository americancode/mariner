package vault

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
)

// UserPreferenceTableRepository owns the user_preferences table.
type UserPreferenceTableRepository struct{ db *sqlx.DB }

func newUserPreferenceTableRepository(db *sqlx.DB) *UserPreferenceTableRepository {
	return &UserPreferenceTableRepository{db: db}
}

func (r *UserPreferenceTableRepository) Get(userID string) (map[string]bool, error) {
	var raw string
	err := r.db.Get(&raw, r.db.Rebind("SELECT settings_json FROM user_preferences WHERE user_id = ?"), userID)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	settings := map[string]bool{}
	return settings, json.Unmarshal([]byte(raw), &settings)
}

func (r *UserPreferenceTableRepository) Save(userID string, settings map[string]bool) error {
	raw, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(r.db.Rebind(`INSERT INTO user_preferences(user_id, settings_json, updated_at) VALUES(?,?,?) ON CONFLICT(user_id) DO UPDATE SET settings_json=excluded.settings_json, updated_at=excluded.updated_at`), userID, string(raw), time.Now().UTC().Format(time.RFC3339))
	return err
}
