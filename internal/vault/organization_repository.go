package vault

import (
	"errors"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
)

// OrganizationTableRepository owns encrypted persistence for organizations.
type OrganizationTableRepository struct {
	db  *sqlx.DB
	mu  *sync.Mutex
	key *string
}

func newOrganizationTableRepository(db *sqlx.DB, mu *sync.Mutex, key *string) *OrganizationTableRepository {
	return &OrganizationTableRepository{db: db, mu: mu, key: key}
}

func (r *OrganizationTableRepository) List() (map[string]Organization, error) {
	if *r.key == "" {
		return map[string]Organization{}, nil
	}
	rows, err := r.db.Queryx("SELECT id, encrypted FROM organizations ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]Organization{}
	for rows.Next() {
		var id, encrypted string
		if err := rows.Scan(&id, &encrypted); err != nil {
			return nil, err
		}
		var organization Organization
		if err := decryptOrganization(encrypted, *r.key, &organization); err != nil {
			return nil, err
		}
		result[id] = organization
	}
	return result, rows.Err()
}

func (r *OrganizationTableRepository) Save(organization Organization) error {
	if *r.key == "" {
		return errors.New("organization encryption key is not configured")
	}
	if organization.ID == "" || organization.Name == "" {
		return errors.New("organization id and name are required")
	}
	if organization.Groups == nil {
		organization.Groups = []string{}
	}
	if organization.Connections == nil {
		organization.Connections = map[string]OrganizationConnection{}
	}
	encrypted, err := encryptOrganization(organization, *r.key)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err = r.db.Exec(r.db.Rebind(`INSERT INTO organizations(id, encrypted, updated_at) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET encrypted=excluded.encrypted, updated_at=excluded.updated_at`), organization.ID, encrypted, time.Now().UTC().Format(time.RFC3339))
	return err
}

func (r *OrganizationTableRepository) Delete(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := r.db.Exec(r.db.Rebind("DELETE FROM organizations WHERE id = ?"), id)
	return err
}
