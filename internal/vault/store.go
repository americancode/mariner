package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/jmoiron/sqlx"
	"golang.org/x/crypto/argon2"
	"mariner/internal/database/migrations"
	"mariner/internal/model"
)

// Connection remains an alias during the migration so existing vault and S3
// callers can move to the canonical domain model without a storage-format
// change.
type Connection = model.Connection
type Organization = model.Organization
type OrganizationConnection = model.OrganizationConnection

// Data remains an alias during the migration so the encrypted vault JSON
// format and existing callers remain unchanged.
type Data = model.Data
type Settings = model.Settings
type envelope struct{ Salt, Nonce, Ciphertext string }
type Store struct {
	db              *sqlx.DB
	mu              sync.Mutex
	vaults          *VaultTableRepository
	organizations   *OrganizationTableRepository
	preferences     *UserPreferenceTableRepository
	application     *ApplicationSettingsTableRepository
	organizationKey string
	audit           *AuditEventTableRepository
}

type AuditEvent = model.AuditEvent
type AuditFilter = model.AuditFilter
type AuditPage = model.AuditPage
type AuditRecord = model.AuditRecord

// VaultRepository returns the table-specific repository used by new API
// use-cases. Store methods remain as a compatibility façade for endpoints
// that have not migrated yet.
func (s *Store) VaultRepository() *VaultTableRepository { return s.vaults }

func (s *Store) OrganizationRepository() *OrganizationTableRepository {
	return s.organizations
}

func (s *Store) AuditRepository() *AuditEventTableRepository { return s.audit }

func (s *Store) UserPreferenceRepository() *UserPreferenceTableRepository {
	return s.preferences
}

func (s *Store) ApplicationSettingsRepository() *ApplicationSettingsTableRepository {
	return s.application
}

func (s *Store) AuditCursor() (string, string, error) {
	return s.audit.Cursor()
}

func (s *Store) ListAuditAfter(occurredAt, eventID string, limit int) ([]AuditRecord, error) {
	return s.audit.ListAfter(occurredAt, eventID, limit)
}

func OpenDatabase(driver, dir, databaseURL string) (*Store, error) {
	if driver == "" || driver == "sqlite" {
		return Open(dir)
	}
	if driver != "postgres" {
		return nil, errors.New("unsupported database driver: " + driver)
	}
	if databaseURL == "" {
		return nil, errors.New("DATABASE_URL is required when DATABASE_DRIVER=postgres")
	}
	db, err := sqlx.Open("pgx", databaseURL)
	if err != nil {
		return nil, err
	}
	store := &Store{db: db}
	store.vaults = newVaultTableRepository(db, &store.mu)
	store.organizations = newOrganizationTableRepository(db, &store.mu, &store.organizationKey)
	store.preferences = newUserPreferenceTableRepository(db)
	store.application = newApplicationSettingsTableRepository(db, &store.mu)
	store.audit = newAuditEventTableRepository(db, &store.mu)
	if err = store.migrate("postgres"); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	db, err := sqlx.Open("sqlite", filepath.Join(dir, "mariner.db"))
	if err != nil {
		return nil, err
	}
	store := &Store{db: db}
	store.vaults = newVaultTableRepository(db, &store.mu)
	store.organizations = newOrganizationTableRepository(db, &store.mu, &store.organizationKey)
	store.preferences = newUserPreferenceTableRepository(db)
	store.application = newApplicationSettingsTableRepository(db, &store.mu)
	store.audit = newAuditEventTableRepository(db, &store.mu)
	if err = store.migrate("sqlite"); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) migrate(driver string) error {
	return migrations.Up(s.db.DB, driver)
}
func (s *Store) GetUserPreferences(userID string) (map[string]bool, error) {
	return s.preferences.Get(userID)
}
func (s *Store) SaveUserPreferences(userID string, settings map[string]bool) error {
	return s.preferences.Save(userID, settings)
}
func (s *Store) SetOrganizationEncryptionKey(key string) { s.organizationKey = key }

func (s *Store) ListOrganizations() (map[string]Organization, error) {
	return s.organizations.List()
}

func (s *Store) SaveOrganization(organization Organization) error {
	return s.organizations.Save(organization)
}

func (s *Store) DeleteOrganization(id string) error {
	return s.organizations.Delete(id)
}

func organizationCipher(key string) (cipher.AEAD, error) {
	sum := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func encryptOrganization(value Organization, key string) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	gcm, err := organizationCipher(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(append(nonce, gcm.Seal(nil, nonce, raw, nil)...)), nil
}
func decryptOrganization(encoded, key string, target *Organization) error {
	payload, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return err
	}
	gcm, err := organizationCipher(key)
	if err != nil {
		return err
	}
	if len(payload) < gcm.NonceSize() {
		return errors.New("invalid encrypted organization")
	}
	raw, err := gcm.Open(nil, payload[:gcm.NonceSize()], payload[gcm.NonceSize():], nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}
func (s *Store) Exists(userID string) (bool, error) {
	return s.vaults.Exists(userID)
}
func (s *Store) Load(userID, password string) (Data, bool, error) {
	return s.vaults.Load(userID, password)
}
func (s *Store) Save(userID, password string, data Data) error {
	return s.vaults.Save(userID, password, data)
}
func (s *Store) Delete(userID string) error {
	return s.vaults.Delete(userID)
}
func (s *Store) AppendAudit(event AuditEvent) (string, error) {
	return s.audit.Append(event)
}

func (s *Store) ListAudit(filter AuditFilter) (AuditPage, error) {
	return s.audit.List(filter)
}

func (s *Store) ListAuditActions() ([]string, error) {
	return s.audit.ListActions()
}
func (s *Store) Close() error { return s.db.Close() }

func key(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
}
func encrypt(data Data, password string) (envelope, error) {
	salt, nonce := make([]byte, 16), make([]byte, 12)
	if _, err := rand.Read(salt); err != nil {
		return envelope{}, err
	}
	if _, err := rand.Read(nonce); err != nil {
		return envelope{}, err
	}
	block, err := aes.NewCipher(key(password, salt))
	if err != nil {
		return envelope{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return envelope{}, err
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return envelope{}, err
	}
	return envelope{base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(nonce), base64.RawStdEncoding.EncodeToString(gcm.Seal(nil, nonce, raw, nil))}, nil
}
func decrypt(e envelope, password string) (Data, error) {
	salt, err := base64.RawStdEncoding.DecodeString(e.Salt)
	if err != nil {
		return Data{}, err
	}
	nonce, err := base64.RawStdEncoding.DecodeString(e.Nonce)
	if err != nil {
		return Data{}, err
	}
	ciphertext, err := base64.RawStdEncoding.DecodeString(e.Ciphertext)
	if err != nil {
		return Data{}, err
	}
	block, err := aes.NewCipher(key(password, salt))
	if err != nil {
		return Data{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return Data{}, err
	}
	raw, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return Data{}, errors.New("incorrect master password")
	}
	var data Data
	return data, json.Unmarshal(raw, &data)
}
