package repository

import "mariner/internal/model"

// VaultRepository persists encrypted per-user vault documents. The API layer
// receives the decrypted model; encryption remains an implementation concern
// of the repository/service boundary during this migration.
type VaultRepository interface {
	Load(userID, password string) (model.Data, bool, error)
	Save(userID, password string, data model.Data) error
}

type OrganizationRepository interface {
	List() (map[string]model.Organization, error)
	Save(organization model.Organization) error
	Delete(id string) error
}

type AuditEventRepository interface {
	Cursor() (occurredAt, eventID string, err error)
	ListAfter(occurredAt, eventID string, limit int) ([]model.AuditRecord, error)
	Append(event model.AuditEvent) (string, error)
	List(filter model.AuditFilter) (model.AuditPage, error)
	ListActions() ([]string, error)
}

type UserPreferenceRepository interface {
	Get(userID string) (map[string]bool, error)
	Save(userID string, settings map[string]bool) error
}
