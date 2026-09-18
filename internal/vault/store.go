package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	"golang.org/x/crypto/argon2"
)

type Connection struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name"`
	Bucket    string `json:"bucket"`
	Region    string `json:"region"`
	Endpoint  string `json:"endpoint"`
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
	Prefix    string `json:"prefix"`
}
type Organization struct {
	ID          string                            `json:"id"`
	Name        string                            `json:"name"`
	Groups      []string                          `json:"groups"`
	Provisioned bool                              `json:"provisioned"`
	Connections map[string]OrganizationConnection `json:"connections"`
}
type OrganizationConnection struct {
	Connection
	AccessKeyEnv string `json:"accessKeyEnv,omitempty"`
	SecretKeyEnv string `json:"secretKeyEnv,omitempty"`
}
type Data struct {
	Connections []Connection `json:"connections"`
	Settings    Settings     `json:"settings"`
}
type Settings struct {
	Theme string `json:"theme,omitempty"`
}
type envelope struct{ Salt, Nonce, Ciphertext string }
type Store struct {
	db              *sqlx.DB
	mu              sync.Mutex
	writeDB         *sqlx.DB
	organizationKey string
}

type AuditEvent struct {
	ID           string `json:"event_id"`
	OccurredAt   string `json:"occurred_at"`
	Action       string `json:"action"`
	Result       string `json:"result"`
	UserID       string `json:"user_id,omitempty"`
	UserName     string `json:"user_name,omitempty"`
	Organization string `json:"organization,omitempty"`
	ConnectionID string `json:"connection_id,omitempty"`
	Bucket       string `json:"bucket,omitempty"`
	ObjectKey    string `json:"object_key,omitempty"`
	FileSHA256   string `json:"file_sha256,omitempty"`
	RequestID    string `json:"request_id,omitempty"`
	Error        string `json:"error,omitempty"`
}

type AuditFilter struct {
	User, Bucket, Action, Start, End string
	Limit, Offset                    int
}

type AuditPage struct {
	Events  []json.RawMessage
	HasMore bool
}
type AuditRecord struct {
	ID, OccurredAt, JSON string
}

type SessionRecord struct {
	UserID, UserName, GroupsJSON, PasswordCiphertext string
	ExpiresAt, LastSeenAt                            time.Time
}

type MultipartUpload struct {
	UploadID, UserID, ConnectionID, Bucket, Key, Status string
	NextPart                                            int32
	PartsJSON, HashState                                string
	ConnectionCiphertext                                string
}

func (s *Store) AuditCursor() (string, string, error) {
	var occurredAt, eventID string
	err := s.db.QueryRowx("SELECT occurred_at, event_id FROM audit_events ORDER BY occurred_at DESC, event_id DESC LIMIT 1").Scan(&occurredAt, &eventID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return occurredAt, eventID, err
}

func (s *Store) ListAuditAfter(occurredAt, eventID string, limit int) ([]AuditRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `SELECT event_id, occurred_at, event_json FROM audit_events
		WHERE occurred_at > ? OR (occurred_at = ? AND event_id > ?)
		ORDER BY occurred_at ASC, event_id ASC LIMIT ?`
	rows, err := s.db.Queryx(s.db.Rebind(query), occurredAt, occurredAt, eventID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]AuditRecord, 0, limit)
	for rows.Next() {
		var id, at, raw string
		if err := rows.Scan(&id, &at, &raw); err != nil {
			return nil, err
		}
		events = append(events, AuditRecord{ID: id, OccurredAt: at, JSON: raw})
	}
	return events, rows.Err()
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
	store := &Store{db: db, writeDB: db}
	if err = store.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	db, err := sqlx.Open("sqlite", filepath.Join(dir, "mariner.db")+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	writeDB, err := sqlx.Open("sqlite", filepath.Join(dir, "mariner.db")+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		db.Close()
		return nil, err
	}
	writeDB.SetMaxOpenConns(1)
	store := &Store{db: db, writeDB: writeDB}
	if err = store.migrate(); err != nil {
		db.Close()
		writeDB.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) migrate() error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS vaults (user_id TEXT PRIMARY KEY, salt TEXT NOT NULL, nonce TEXT NOT NULL, ciphertext TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS audit_events (event_id TEXT PRIMARY KEY, occurred_at TEXT NOT NULL, event_json TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS organizations (id TEXT PRIMARY KEY, encrypted TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS user_preferences (user_id TEXT PRIMARY KEY, settings_json TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS sessions (id TEXT PRIMARY KEY, user_id TEXT NOT NULL, user_name TEXT NOT NULL, groups_json TEXT NOT NULL, password_ciphertext TEXT NOT NULL, expires_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS multipart_uploads (upload_id TEXT PRIMARY KEY, user_id TEXT NOT NULL, connection_id TEXT NOT NULL, bucket TEXT NOT NULL, object_key TEXT NOT NULL, status TEXT NOT NULL, next_part INTEGER NOT NULL, parts_json TEXT NOT NULL, hash_state TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
	} {
		if _, err := s.writeDB.Exec(statement); err != nil {
			return err
		}
	}
	if _, err := s.writeDB.Exec(s.writeDB.Rebind(`ALTER TABLE multipart_uploads ADD COLUMN connection_ciphertext TEXT NOT NULL DEFAULT ''`)); err != nil {
		message := strings.ToLower(err.Error())
		if !strings.Contains(message, "duplicate") && !strings.Contains(message, "already exists") {
			return err
		}
	}
	return nil
}

func (s *Store) SaveSession(id, userID, userName, groupsJSON, passwordCiphertext string, expiresAt time.Time) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.writeDB.Exec(s.writeDB.Rebind(`INSERT INTO sessions(id,user_id,user_name,groups_json,password_ciphertext,expires_at,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET user_id=excluded.user_id,user_name=excluded.user_name,groups_json=excluded.groups_json,password_ciphertext=excluded.password_ciphertext,expires_at=excluded.expires_at,updated_at=excluded.updated_at`), id, userID, userName, groupsJSON, passwordCiphertext, expiresAt.UTC().Format(time.RFC3339Nano), now)
	return err
}

func (s *Store) LoadSession(id string) (userID, userName, groupsJSON, passwordCiphertext string, expiresAt, lastSeenAt time.Time, found bool, err error) {
	var record SessionRecord
	var expires, lastSeen string
	err = s.db.QueryRowx(s.db.Rebind(`SELECT user_id,user_name,groups_json,password_ciphertext,expires_at,updated_at FROM sessions WHERE id=?`), id).Scan(&record.UserID, &record.UserName, &record.GroupsJSON, &record.PasswordCiphertext, &expires, &lastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", "", time.Time{}, time.Time{}, false, nil
	}
	if err != nil {
		return "", "", "", "", time.Time{}, time.Time{}, false, err
	}
	record.ExpiresAt, err = time.Parse(time.RFC3339Nano, expires)
	if err != nil {
		return "", "", "", "", time.Time{}, time.Time{}, false, err
	}
	record.LastSeenAt, err = time.Parse(time.RFC3339Nano, lastSeen)
	return record.UserID, record.UserName, record.GroupsJSON, record.PasswordCiphertext, record.ExpiresAt, record.LastSeenAt, err == nil, err
}

func (s *Store) TouchSessionIfStale(id string, at, before time.Time) error {
	_, err := s.writeDB.Exec(s.writeDB.Rebind(`UPDATE sessions SET updated_at=? WHERE id=? AND updated_at < ?`), at.UTC().Format(time.RFC3339Nano), id, before.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) DeleteSession(id string) error {
	_, err := s.writeDB.Exec(s.writeDB.Rebind(`DELETE FROM sessions WHERE id=?`), id)
	return err
}

func (s *Store) CreateMultipartUpload(upload MultipartUpload) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.writeDB.Exec(s.writeDB.Rebind(`INSERT INTO multipart_uploads(upload_id,user_id,connection_id,bucket,object_key,status,next_part,parts_json,hash_state,created_at,updated_at,connection_ciphertext) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`), upload.UploadID, upload.UserID, upload.ConnectionID, upload.Bucket, upload.Key, "active", 1, "[]", upload.HashState, now, now, upload.ConnectionCiphertext)
	return err
}

func (s *Store) LoadMultipartUpload(id string) (MultipartUpload, bool, error) {
	var upload MultipartUpload
	err := s.db.QueryRowx(s.db.Rebind(`SELECT upload_id,user_id,connection_id,bucket,object_key,status,next_part,parts_json,hash_state,connection_ciphertext FROM multipart_uploads WHERE upload_id=?`), id).Scan(&upload.UploadID, &upload.UserID, &upload.ConnectionID, &upload.Bucket, &upload.Key, &upload.Status, &upload.NextPart, &upload.PartsJSON, &upload.HashState, &upload.ConnectionCiphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return upload, false, nil
	}
	return upload, err == nil, err
}

func (s *Store) ListStaleMultipartUploads(before time.Time) ([]MultipartUpload, error) {
	rows, err := s.db.Queryx(s.db.Rebind(`SELECT upload_id,user_id,connection_id,bucket,object_key,status,next_part,parts_json,hash_state,connection_ciphertext FROM multipart_uploads WHERE status IN ('active','completing','cleaning') AND updated_at < ? ORDER BY updated_at ASC LIMIT 100`), before.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var uploads []MultipartUpload
	for rows.Next() {
		var upload MultipartUpload
		if err := rows.Scan(&upload.UploadID, &upload.UserID, &upload.ConnectionID, &upload.Bucket, &upload.Key, &upload.Status, &upload.NextPart, &upload.PartsJSON, &upload.HashState, &upload.ConnectionCiphertext); err != nil {
			return nil, err
		}
		uploads = append(uploads, upload)
	}
	return uploads, rows.Err()
}

func (s *Store) ClaimMultipartCleanup(id string, before time.Time) (bool, error) {
	result, err := s.writeDB.Exec(s.writeDB.Rebind(`UPDATE multipart_uploads SET status='cleaning' WHERE upload_id=? AND status IN ('active','completing','cleaning') AND updated_at < ?`), id, before.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (s *Store) SaveMultipartPart(id string, expectedPart int32, partsJSON, hashState string) (bool, error) {
	result, err := s.writeDB.Exec(s.writeDB.Rebind(`UPDATE multipart_uploads SET next_part=next_part+1,parts_json=?,hash_state=?,updated_at=? WHERE upload_id=? AND status='active' AND next_part=?`), partsJSON, hashState, time.Now().UTC().Format(time.RFC3339Nano), id, expectedPart)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (s *Store) ClaimMultipartComplete(id string) (MultipartUpload, bool, error) {
	result, err := s.writeDB.Exec(s.writeDB.Rebind(`UPDATE multipart_uploads SET status='completing',updated_at=? WHERE upload_id=? AND status='active' AND next_part>1`), time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return MultipartUpload{}, false, err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return MultipartUpload{}, false, err
	}
	return s.LoadMultipartUpload(id)
}

func (s *Store) FinishMultipartUpload(id string, success bool) error {
	status := "active"
	if success {
		status = "complete"
	}
	_, err := s.writeDB.Exec(s.writeDB.Rebind(`UPDATE multipart_uploads SET status=?,updated_at=? WHERE upload_id=? AND status='completing'`), status, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

func (s *Store) DeleteMultipartUpload(id string) error {
	_, err := s.writeDB.Exec(s.writeDB.Rebind(`DELETE FROM multipart_uploads WHERE upload_id=?`), id)
	return err
}
func (s *Store) GetUserPreferences(userID string) (map[string]bool, error) {
	var raw string
	err := s.db.Get(&raw, s.db.Rebind("SELECT settings_json FROM user_preferences WHERE user_id = ?"), userID)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	settings := map[string]bool{}
	return settings, json.Unmarshal([]byte(raw), &settings)
}
func (s *Store) SaveUserPreferences(userID string, settings map[string]bool) error {
	raw, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	_, err = s.writeDB.Exec(s.writeDB.Rebind(`INSERT INTO user_preferences(user_id, settings_json, updated_at) VALUES(?,?,?) ON CONFLICT(user_id) DO UPDATE SET settings_json=excluded.settings_json, updated_at=excluded.updated_at`), userID, string(raw), time.Now().UTC().Format(time.RFC3339))
	return err
}
func (s *Store) SetOrganizationEncryptionKey(key string) { s.organizationKey = key }

func (s *Store) ListOrganizations() (map[string]Organization, error) {
	if s.organizationKey == "" {
		return map[string]Organization{}, nil
	}
	rows, err := s.db.Queryx("SELECT id, encrypted FROM organizations ORDER BY id")
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
		if err := decryptOrganization(encrypted, s.organizationKey, &organization); err != nil {
			return nil, err
		}
		result[id] = organization
	}
	return result, rows.Err()
}

func (s *Store) SaveOrganization(organization Organization) error {
	if s.organizationKey == "" {
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
	encrypted, err := encryptOrganization(organization, s.organizationKey)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.writeDB.Exec(s.writeDB.Rebind(`INSERT INTO organizations(id, encrypted, updated_at) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET encrypted=excluded.encrypted, updated_at=excluded.updated_at`), organization.ID, encrypted, time.Now().UTC().Format(time.RFC3339))
	return err
}

func (s *Store) DeleteOrganization(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.writeDB.Exec(s.writeDB.Rebind("DELETE FROM organizations WHERE id = ?"), id)
	return err
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
	var count int
	err := s.db.Get(&count, s.db.Rebind("SELECT count(*) FROM vaults WHERE user_id = ?"), userID)
	return count > 0, err
}
func (s *Store) Load(userID, password string) (Data, bool, error) {
	var e envelope
	err := s.db.QueryRowx(s.db.Rebind("SELECT salt, nonce, ciphertext FROM vaults WHERE user_id = ?"), userID).Scan(&e.Salt, &e.Nonce, &e.Ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return Data{}, false, nil
	}
	if err != nil {
		return Data{}, false, err
	}
	data, err := decrypt(e, password)
	return data, true, err
}
func (s *Store) Save(userID, password string, data Data) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := encrypt(data, password)
	if err != nil {
		return err
	}
	_, err = s.writeDB.Exec(s.writeDB.Rebind(`INSERT INTO vaults(user_id,salt,nonce,ciphertext,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(user_id) DO UPDATE SET salt=excluded.salt,nonce=excluded.nonce,ciphertext=excluded.ciphertext,updated_at=excluded.updated_at`), userID, e.Salt, e.Nonce, e.Ciphertext, time.Now().UTC().Format(time.RFC3339))
	return err
}
func (s *Store) Delete(userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.writeDB.Exec(s.writeDB.Rebind("DELETE FROM vaults WHERE user_id = ?"), userID)
	return err
}
func (s *Store) AppendAudit(event AuditEvent) (string, error) {
	raw, err := json.Marshal(event)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.writeDB.Exec(s.writeDB.Rebind(`INSERT INTO audit_events(event_id, occurred_at, event_json) VALUES(?,?,?)`), event.ID, event.OccurredAt, string(raw))
	return string(raw), err
}

func (s *Store) ListAudit(filter AuditFilter) (AuditPage, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	jsonField := func(name string) string {
		if s.db.DriverName() == "pgx" {
			return "(event_json::jsonb ->> '" + name + "')"
		}
		return "json_extract(event_json, '$." + name + "')"
	}
	query := "SELECT event_json FROM audit_events WHERE 1=1"
	args := make([]any, 0, 6)
	if filter.User != "" {
		query += " AND (" + jsonField("user_id") + " = ? OR " + jsonField("user_name") + " = ?)"
		args = append(args, filter.User, filter.User)
	}
	for _, condition := range []struct{ value, expression string }{
		{filter.Bucket, jsonField("bucket")},
		{filter.Action, jsonField("action")},
	} {
		if condition.value == "" {
			continue
		}
		query += " AND " + condition.expression + " = ?"
		args = append(args, condition.value)
	}
	if filter.Start != "" {
		query += " AND occurred_at >= ?"
		args = append(args, filter.Start)
	}
	if filter.End != "" {
		query += " AND occurred_at <= ?"
		args = append(args, filter.End)
	}
	// Include the event ID as a tie-breaker so offset pagination remains stable
	// when multiple events share the same timestamp.
	query += " ORDER BY occurred_at DESC, event_id DESC LIMIT ? OFFSET ?"
	args = append(args, limit+1, filter.Offset)
	rows, err := s.db.Queryx(s.db.Rebind(query), args...)
	if err != nil {
		return AuditPage{}, err
	}
	defer rows.Close()
	// Avoid allocating from the caller-provided limit. The limit is enforced
	// above and the result slice can grow only as rows are returned.
	page := AuditPage{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return AuditPage{}, err
		}
		if len(page.Events) == limit {
			page.HasMore = true
			break
		}
		page.Events = append(page.Events, json.RawMessage(raw))
	}
	return page, rows.Err()
}

func (s *Store) ListAuditActions() ([]string, error) {
	field := "json_extract(event_json, '$.action')"
	if s.db.DriverName() == "pgx" {
		field = "(event_json::jsonb ->> 'action')"
	}
	var actions []string
	if err := s.db.Select(&actions, "SELECT DISTINCT "+field+" FROM audit_events WHERE "+field+" IS NOT NULL AND "+field+" <> '' ORDER BY 1"); err != nil {
		return nil, err
	}
	return actions, nil
}
func (s *Store) Close() error {
	err := s.db.Close()
	if s.writeDB != s.db {
		if closeErr := s.writeDB.Close(); err == nil {
			err = closeErr
		}
	}
	return err
}

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
