package vault

import (
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
)

// VaultTableRepository owns persistence for the vaults table. It deliberately
// stores encrypted envelope columns; encryption/decryption is kept in this
// package boundary and is not exposed to HTTP or API callers.
type VaultTableRepository struct {
	db *sqlx.DB
	mu *sync.Mutex
}

func newVaultTableRepository(db *sqlx.DB, mu *sync.Mutex) *VaultTableRepository {
	return &VaultTableRepository{db: db, mu: mu}
}

func (r *VaultTableRepository) Exists(userID string) (bool, error) {
	var count int
	err := r.db.Get(&count, r.db.Rebind("SELECT count(*) FROM vaults WHERE user_id = ?"), userID)
	return count > 0, err
}

func (r *VaultTableRepository) Load(userID, password string) (Data, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var e envelope
	err := r.db.QueryRowx(r.db.Rebind("SELECT salt, nonce, ciphertext FROM vaults WHERE user_id = ?"), userID).Scan(&e.Salt, &e.Nonce, &e.Ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return Data{}, false, nil
	}
	if err != nil {
		return Data{}, false, err
	}
	data, err := decrypt(e, password)
	return data, true, err
}

func (r *VaultTableRepository) Save(userID, password string, data Data) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, err := encrypt(data, password)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(r.db.Rebind(`INSERT INTO vaults(user_id,salt,nonce,ciphertext,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(user_id) DO UPDATE SET salt=excluded.salt,nonce=excluded.nonce,ciphertext=excluded.ciphertext,updated_at=excluded.updated_at`), userID, e.Salt, e.Nonce, e.Ciphertext, time.Now().UTC().Format(time.RFC3339))
	return err
}

func (r *VaultTableRepository) Delete(userID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := r.db.Exec(r.db.Rebind("DELETE FROM vaults WHERE user_id = ?"), userID)
	return err
}
