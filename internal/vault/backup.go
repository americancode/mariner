package vault

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const ExportBundleVersion = 1

type ExportBundle struct {
	Format        string               `json:"format"`
	Version       int                  `json:"version"`
	ExportedAt    string               `json:"exportedAt"`
	Vaults        []VaultBackup        `json:"vaults"`
	Organizations []OrganizationBackup `json:"organizations"`
	Preferences   []PreferenceBackup   `json:"preferences"`
	AuditEvents   []AuditBackup        `json:"auditEvents"`
}
type VaultBackup struct {
	UserID     string `db:"user_id" json:"userId"`
	Salt       string `db:"salt"`
	Nonce      string `db:"nonce"`
	Ciphertext string `db:"ciphertext"`
	UpdatedAt  string `db:"updated_at" json:"updatedAt"`
}
type OrganizationBackup struct {
	ID        string `db:"id"`
	Encrypted string `db:"encrypted"`
	UpdatedAt string `db:"updated_at" json:"updatedAt"`
}
type PreferenceBackup struct {
	UserID       string `db:"user_id" json:"userId"`
	SettingsJSON string `db:"settings_json" json:"settingsJson"`
	UpdatedAt    string `db:"updated_at" json:"updatedAt"`
}
type AuditBackup struct {
	EventID    string `db:"event_id" json:"eventId"`
	OccurredAt string `db:"occurred_at" json:"occurredAt"`
	EventJSON  string `db:"event_json" json:"eventJson"`
}

func (s *Store) ExportBundle() (ExportBundle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	bundle := ExportBundle{Format: "mariner-export", Version: ExportBundleVersion, ExportedAt: time.Now().UTC().Format(time.RFC3339Nano), Vaults: []VaultBackup{}, Organizations: []OrganizationBackup{}, Preferences: []PreferenceBackup{}, AuditEvents: []AuditBackup{}}
	if err := s.db.Select(&bundle.Vaults, "SELECT user_id, salt, nonce, ciphertext, updated_at FROM vaults ORDER BY user_id"); err != nil {
		return ExportBundle{}, err
	}
	if err := s.db.Select(&bundle.Organizations, "SELECT id, encrypted, updated_at FROM organizations ORDER BY id"); err != nil {
		return ExportBundle{}, err
	}
	if err := s.db.Select(&bundle.Preferences, "SELECT user_id, settings_json, updated_at FROM user_preferences ORDER BY user_id"); err != nil {
		return ExportBundle{}, err
	}
	if err := s.db.Select(&bundle.AuditEvents, "SELECT event_id, occurred_at, event_json FROM audit_events ORDER BY occurred_at, event_id"); err != nil {
		return ExportBundle{}, err
	}
	return bundle, nil
}

func (s *Store) ImportBundle(bundle ExportBundle) error {
	if err := validateExportBundle(bundle); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Beginx()
	if err != nil {
		return err
	}
	fail := func(err error) error { _ = tx.Rollback(); return err }
	for _, query := range []string{"DELETE FROM audit_events", "DELETE FROM user_preferences", "DELETE FROM organizations", "DELETE FROM vaults"} {
		if _, err := tx.Exec(tx.Rebind(query)); err != nil {
			return fail(err)
		}
	}
	for _, record := range bundle.Vaults {
		if _, err := tx.Exec(tx.Rebind("INSERT INTO vaults(user_id,salt,nonce,ciphertext,updated_at) VALUES(?,?,?,?,?)"), record.UserID, record.Salt, record.Nonce, record.Ciphertext, record.UpdatedAt); err != nil {
			return fail(err)
		}
	}
	for _, record := range bundle.Organizations {
		if _, err := tx.Exec(tx.Rebind("INSERT INTO organizations(id,encrypted,updated_at) VALUES(?,?,?)"), record.ID, record.Encrypted, record.UpdatedAt); err != nil {
			return fail(err)
		}
	}
	for _, record := range bundle.Preferences {
		if _, err := tx.Exec(tx.Rebind("INSERT INTO user_preferences(user_id,settings_json,updated_at) VALUES(?,?,?)"), record.UserID, record.SettingsJSON, record.UpdatedAt); err != nil {
			return fail(err)
		}
	}
	for _, record := range bundle.AuditEvents {
		if _, err := tx.Exec(tx.Rebind("INSERT INTO audit_events(event_id,occurred_at,event_json) VALUES(?,?,?)"), record.EventID, record.OccurredAt, record.EventJSON); err != nil {
			return fail(err)
		}
	}
	return tx.Commit()
}

func validateExportBundle(bundle ExportBundle) error {
	if bundle.Format != "mariner-export" || bundle.Version != ExportBundleVersion {
		return fmt.Errorf("unsupported Mariner export format or version")
	}
	if bundle.ExportedAt == "" {
		return errors.New("exportedAt is required")
	}
	if _, err := time.Parse(time.RFC3339Nano, bundle.ExportedAt); err != nil {
		return errors.New("exportedAt is invalid")
	}
	seen := map[string]bool{}
	for _, record := range bundle.Vaults {
		if record.UserID == "" || seen["v:"+record.UserID] {
			return errors.New("export contains duplicate or empty vault users")
		}
		seen["v:"+record.UserID] = true
	}
	seen = map[string]bool{}
	for _, record := range bundle.Organizations {
		if record.ID == "" || seen[record.ID] {
			return errors.New("export contains duplicate or empty organizations")
		}
		seen[record.ID] = true
	}
	seen = map[string]bool{}
	for _, record := range bundle.Preferences {
		if record.UserID == "" || seen[record.UserID] {
			return errors.New("export contains duplicate or empty preferences")
		}
		seen[record.UserID] = true
	}
	seen = map[string]bool{}
	for _, record := range bundle.AuditEvents {
		if record.EventID == "" || seen[record.EventID] {
			return errors.New("export contains duplicate or empty audit events")
		}
		seen[record.EventID] = true
		var value any
		if json.Unmarshal([]byte(record.EventJSON), &value) != nil {
			return errors.New("export contains invalid audit JSON")
		}
	}
	return nil
}
