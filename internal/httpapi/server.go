package httpapi

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"log"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/go-chi/chi/v5"
	"periscope/internal/audit"
	"periscope/internal/auth"
	"periscope/internal/config"
	s3client "periscope/internal/s3"
	"periscope/internal/vault"
)

type Server struct {
	Auth            *auth.Service
	Vault           *vault.Store
	Audit           *audit.Logger
	AuditAdminGroup string
	Organizations   map[string]config.Organization
	uploadMu        sync.Mutex
	uploads         map[string]*uploadState
}

type uploadState struct {
	UserID     string
	Connection vault.Connection
	Bucket     string
	Key        string
	UploadID   string
	Hash       hash.Hash
	Parts      []types.CompletedPart
	NextPart   int32
}

func (s *Server) audit(session auth.Session, action, result string, fields map[string]string) {
	if s.Audit == nil {
		return
	}
	event := vault.AuditEvent{Action: action, Result: result, UserID: session.User.ID, UserName: session.User.Name}
	for key, value := range fields {
		switch key {
		case "organization":
			event.Organization = value
		case "connection_id":
			event.ConnectionID = value
		case "bucket":
			event.Bucket = value
		case "object_key":
			event.ObjectKey = value
		case "file_sha256":
			event.FileSHA256 = value
		case "error":
			event.Error = value
		}
	}
	if err := s.Audit.Write(event); err != nil {
		log.Printf("audit write failed action=%s: %v", action, err)
	}
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/auth/login", s.login)
	r.Get("/auth/callback", s.callback)
	r.Get("/auth/logout", s.logout)
	r.Get("/api/me", s.me)
	r.Get("/api/admin/audit", s.adminAudit)
	r.Get("/api/admin/audit/actions", s.adminAuditActions)
	r.Get("/api/admin/preferences", s.adminPreferences)
	r.Put("/api/admin/preferences", s.updateAdminPreferences)
	r.Get("/api/admin/organizations", s.adminOrganizations)
	r.Post("/api/admin/organizations", s.createAdminOrganization)
	r.Put("/api/admin/organizations", s.updateAdminOrganization)
	r.Delete("/api/admin/organizations", s.deleteAdminOrganization)
	r.Get("/api/vault/status", s.status)
	r.Post("/api/vault/unlock", s.unlock)
	r.Post("/api/vault/lock", s.lock)
	r.Delete("/api/vault", s.destroyVault)
	r.Get("/api/settings", s.settings)
	r.Put("/api/settings", s.updateSettings)
	r.Get("/api/connections", s.connections)
	r.Post("/api/connections", s.addConnection)
	r.Post("/api/connections/test", s.testConnection)
	r.Put("/api/connections", s.updateConnection)
	r.Delete("/api/connections", s.deleteConnection)
	r.Post("/api/folders", s.createFolder)
	r.Get("/api/browse", s.browse)
	r.Get("/api/file", s.file)
	r.Delete("/api/file", s.deleteFile)
	r.Post("/api/upload", s.upload)
	r.Post("/api/upload/init", s.uploadInit)
	r.Put("/api/upload/part", s.uploadPart)
	r.Post("/api/upload/complete", s.uploadComplete)
	r.Delete("/api/upload", s.uploadAbort)
	r.Get("/api/download", s.download)
	// Admin is a client-side route. Serve the SPA entrypoint so direct loads
	// and browser refreshes do not look for a physical /web/admin file.
	spa := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "/web/index.html")
	})
	r.Get("/admin", spa)
	r.Get("/admin/", spa)
	r.Get("/admin/*", spa)
	r.Handle("/*", http.FileServer(http.Dir("/web")))
	return r
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) { s.Auth.Login(w, r) }

func (s *Server) adminAudit(w http.ResponseWriter, r *http.Request) {
	session, _, err := s.session(r)
	if err != nil {
		fail(w, http.StatusUnauthorized, err)
		return
	}
	if s.AuditAdminGroup == "" || !hasGroup(session.User.Groups, []string{s.AuditAdminGroup}) {
		fail(w, http.StatusForbidden, errors.New("audit administrator access required"))
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	start, end := auditTimeBoundary(r.URL.Query().Get("start"), false), auditTimeBoundary(r.URL.Query().Get("end"), true)
	page, err := s.Vault.ListAudit(vault.AuditFilter{
		User:   r.URL.Query().Get("user"),
		Bucket: r.URL.Query().Get("bucket"),
		Action: r.URL.Query().Get("action"),
		Start:  start,
		End:    end,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	write(w, map[string]any{"events": page.Events, "nextOffset": offset + len(page.Events), "hasMore": page.HasMore})
}

func (s *Server) adminAuditActions(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.adminAllowed(r); !ok {
		fail(w, http.StatusForbidden, errors.New("administrator access required"))
		return
	}
	actions, err := s.Vault.ListAuditActions()
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	write(w, actions)
}
func (s *Server) adminPreferences(w http.ResponseWriter, r *http.Request) {
	session, ok := s.adminAllowed(r)
	if !ok {
		fail(w, 403, errors.New("administrator access required"))
		return
	}
	preferences, err := s.Vault.GetUserPreferences(session.User.ID)
	if err != nil {
		fail(w, 500, err)
		return
	}
	write(w, preferences)
}
func (s *Server) updateAdminPreferences(w http.ResponseWriter, r *http.Request) {
	session, ok := s.adminAllowed(r)
	if !ok {
		fail(w, 403, errors.New("administrator access required"))
		return
	}
	preferences := map[string]bool{}
	if json.NewDecoder(r.Body).Decode(&preferences) != nil {
		fail(w, 400, errors.New("invalid preferences"))
		return
	}
	if err := s.Vault.SaveUserPreferences(session.User.ID, preferences); err != nil {
		fail(w, 500, err)
		return
	}
	write(w, preferences)
}

func (s *Server) adminAllowed(r *http.Request) (auth.Session, bool) {
	session, _, err := s.session(r)
	return session, err == nil && s.AuditAdminGroup != "" && hasGroup(session.User.Groups, []string{s.AuditAdminGroup})
}
func (s *Server) adminOrganizations(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.adminAllowed(r); !ok {
		fail(w, http.StatusForbidden, errors.New("administrator access required"))
		return
	}
	organizations, err := s.Vault.ListOrganizations()
	if err != nil {
		fail(w, 500, err)
		return
	}
	result := make([]vault.Organization, 0, len(s.Organizations)+len(organizations))
	for _, organization := range s.Organizations {
		result = append(result, publicOrganization(organization))
	}
	for _, organization := range organizations {
		result = append(result, publicOrganization(organization))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	write(w, result)
}
func (s *Server) createAdminOrganization(w http.ResponseWriter, r *http.Request) {
	s.saveAdminOrganization(w, r, false)
}
func (s *Server) updateAdminOrganization(w http.ResponseWriter, r *http.Request) {
	s.saveAdminOrganization(w, r, true)
}
func (s *Server) saveAdminOrganization(w http.ResponseWriter, r *http.Request, update bool) {
	session, ok := s.adminAllowed(r)
	if !ok {
		fail(w, 403, errors.New("administrator access required"))
		return
	}
	var organization vault.Organization
	if json.NewDecoder(r.Body).Decode(&organization) != nil || organization.ID == "" || organization.Name == "" {
		fail(w, 400, errors.New("organization id and name are required"))
		return
	}
	if _, provisioned := s.Organizations[organization.ID]; provisioned {
		fail(w, 409, errors.New("Helm-provisioned organizations are read-only"))
		return
	}
	if update {
		stored, err := s.Vault.ListOrganizations()
		if err != nil {
			fail(w, 500, err)
			return
		}
		if current, exists := stored[organization.ID]; exists {
			mergeOrganizationSecrets(&organization, current)
		}
	}
	for name, connection := range organization.Connections {
		if connection.ID == "" {
			connection.ID = name
		}
		if connection.Name == "" || connection.Bucket == "" || connection.AccessKey == "" || connection.SecretKey == "" {
			fail(w, 400, fmt.Errorf("connection %q requires name, bucket, access key, and secret key", name))
			return
		}
		organization.Connections[name] = connection
	}
	if err := s.Vault.SaveOrganization(organization); err != nil {
		fail(w, 500, err)
		return
	}
	s.audit(session, "organization."+map[bool]string{true: "update", false: "create"}[update], "success", map[string]string{"organization": organization.ID})
	write(w, publicOrganization(organization))
}
func (s *Server) deleteAdminOrganization(w http.ResponseWriter, r *http.Request) {
	session, ok := s.adminAllowed(r)
	if !ok {
		fail(w, 403, errors.New("administrator access required"))
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		fail(w, 400, errors.New("organization id is required"))
		return
	}
	if _, provisioned := s.Organizations[id]; provisioned {
		fail(w, 409, errors.New("Helm-provisioned organizations are read-only"))
		return
	}
	if err := s.Vault.DeleteOrganization(id); err != nil {
		fail(w, 500, err)
		return
	}
	s.audit(session, "organization.delete", "success", map[string]string{"organization": id})
	w.WriteHeader(http.StatusNoContent)
}
func mergeOrganizationSecrets(next *vault.Organization, current vault.Organization) {
	for name, connection := range next.Connections {
		if connection.AccessKey == "" {
			connection.AccessKey = current.Connections[name].AccessKey
		}
		if connection.SecretKey == "" {
			connection.SecretKey = current.Connections[name].SecretKey
		}
		next.Connections[name] = connection
	}
}
func publicOrganization(organization vault.Organization) vault.Organization {
	public := organization
	public.Connections = map[string]vault.OrganizationConnection{}
	for name, connection := range organization.Connections {
		connection.AccessKey, connection.SecretKey = "", ""
		public.Connections[name] = connection
	}
	return public
}

func auditTimeBoundary(value string, end bool) string {
	if value == "" || strings.Contains(value, "T") {
		return value
	}
	if end {
		return value + "T23:59:59.999999999Z"
	}
	return value + "T00:00:00Z"
}

func (s *Server) callback(w http.ResponseWriter, r *http.Request) {
	user, id, err := s.Auth.Callback(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	s.Auth.StartSession(w, id, user)
	s.audit(auth.Session{User: user}, "auth.login", "success", nil)
	http.Redirect(w, r, "/", http.StatusFound)
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if session, _, ok := s.Auth.Current(r); ok {
		s.audit(session, "auth.logout", "success", nil)
	}
	s.Auth.Logout(w, r)
	http.Redirect(w, r, "/", http.StatusFound)
}
func (s *Server) session(r *http.Request) (auth.Session, string, error) {
	session, id, ok := s.Auth.Current(r)
	if !ok {
		return session, id, errors.New("sign in required")
	}
	return session, id, nil
}
func (s *Server) unlocked(r *http.Request) (auth.Session, string, vault.Data, error) {
	session, id, err := s.session(r)
	if err != nil {
		return session, id, vault.Data{}, err
	}
	if session.Password == "" {
		return session, id, vault.Data{}, errors.New("vault is locked")
	}
	data, _, err := s.Vault.Load(session.User.ID, session.Password)
	return session, id, data, err
}
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	session, _, ok := s.Auth.Current(r)
	if !ok {
		write(w, map[string]any{"authenticated": false})
		return
	}
	write(w, map[string]any{"authenticated": true, "name": session.User.Name, "isAdmin": s.AuditAdminGroup != "" && hasGroup(session.User.Groups, []string{s.AuditAdminGroup})})
}
func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	session, _, err := s.session(r)
	if err != nil {
		fail(w, 401, err)
		return
	}
	exists, err := s.Vault.Exists(session.User.ID)
	if err != nil {
		fail(w, 500, err)
		return
	}
	write(w, map[string]bool{"exists": exists})
}
func (s *Server) unlock(w http.ResponseWriter, r *http.Request) {
	session, id, err := s.session(r)
	if err != nil {
		fail(w, 401, err)
		return
	}
	var request struct {
		Password string `json:"password"`
	}
	if json.NewDecoder(r.Body).Decode(&request) != nil || len(request.Password) < 10 {
		fail(w, 400, errors.New("master password must be at least 10 characters"))
		return
	}
	data, exists, err := s.Vault.Load(session.User.ID, request.Password)
	if err != nil {
		fail(w, 401, err)
		return
	}
	if !exists {
		data = vault.Data{}
		if err = s.Vault.Save(session.User.ID, request.Password, data); err != nil {
			fail(w, 500, err)
			return
		}
	}
	s.Auth.SetPassword(id, request.Password)
	s.audit(session, "vault.unlock", "success", nil)
	write(w, publicConnections(s.withOrganizations(session.User, data)))
}
func (s *Server) lock(w http.ResponseWriter, r *http.Request) {
	session, id, err := s.session(r)
	if err != nil {
		fail(w, 401, err)
		return
	}
	s.Auth.Lock(id)
	s.audit(session, "vault.lock", "success", nil)
	write(w, map[string]bool{"ok": true})
}
func (s *Server) destroyVault(w http.ResponseWriter, r *http.Request) {
	session, id, err := s.session(r)
	if err != nil {
		fail(w, 401, err)
		return
	}
	if err := s.Vault.Delete(session.User.ID); err != nil {
		fail(w, 500, err)
		return
	}
	s.Auth.Lock(id)
	s.audit(session, "vault.destroy", "success", nil)
	write(w, map[string]bool{"ok": true})
}
func (s *Server) connections(w http.ResponseWriter, r *http.Request) {
	session, _, data, err := s.unlocked(r)
	if err != nil {
		fail(w, 423, err)
		return
	}
	write(w, publicConnections(s.withOrganizations(session.User, data)))
}
func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	_, _, data, err := s.unlocked(r)
	if err != nil {
		fail(w, 423, err)
		return
	}
	write(w, data.Settings)
}
func (s *Server) updateSettings(w http.ResponseWriter, r *http.Request) {
	session, _, data, err := s.unlocked(r)
	if err != nil {
		fail(w, 423, err)
		return
	}
	var settings vault.Settings
	if json.NewDecoder(r.Body).Decode(&settings) != nil || (settings.Theme != "light" && settings.Theme != "dark") {
		fail(w, 400, errors.New("theme must be light or dark"))
		return
	}
	data.Settings = settings
	if err = s.Vault.Save(session.User.ID, session.Password, data); err != nil {
		fail(w, 500, err)
		return
	}
	write(w, settings)
	s.audit(session, "settings.update", "success", nil)
}
func (s *Server) addConnection(w http.ResponseWriter, r *http.Request) {
	session, _, data, err := s.unlocked(r)
	if err != nil {
		fail(w, 423, err)
		return
	}
	var c vault.Connection
	if json.NewDecoder(r.Body).Decode(&c) != nil || c.Name == "" || c.Bucket == "" {
		fail(w, 400, errors.New("name and bucket are required"))
		return
	}
	if err := validateS3Connection(r.Context(), c); err != nil {
		fail(w, http.StatusBadRequest, fmt.Errorf("connection test failed: %w", err))
		return
	}
	c.ID = randomID()
	data.Connections = append(data.Connections, c)
	if err = s.Vault.Save(session.User.ID, session.Password, data); err != nil {
		fail(w, 500, err)
		return
	}
	write(w, map[string]string{"id": c.ID})
	s.audit(session, "connection.create", "success", map[string]string{"connection_id": c.ID, "bucket": c.Bucket})
}
func (s *Server) testConnection(w http.ResponseWriter, r *http.Request) {
	var c vault.Connection
	if json.NewDecoder(r.Body).Decode(&c) != nil || c.Bucket == "" {
		fail(w, http.StatusBadRequest, errors.New("bucket is required"))
		return
	}
	_, admin := s.adminAllowed(r)
	var data vault.Data
	var err error
	if !(admin && c.AccessKey != "" && c.SecretKey != "") {
		_, _, data, err = s.unlocked(r)
		if err != nil {
			fail(w, 423, err)
			return
		}
	}
	if c.ID != "" {
		for _, existing := range data.Connections {
			if existing.ID == c.ID {
				if c.AccessKey == "" {
					c.AccessKey = existing.AccessKey
				}
				if c.SecretKey == "" {
					c.SecretKey = existing.SecretKey
				}
				break
			}
		}
	}
	if err := validateS3Connection(r.Context(), c); err != nil {
		fail(w, http.StatusBadRequest, fmt.Errorf("connection test failed: %w", err))
		return
	}
	write(w, map[string]bool{"ok": true})
}

func validateS3Connection(ctx context.Context, c vault.Connection) error {
	client, err := s3client.New(ctx, c)
	if err != nil {
		return err
	}
	_, err = client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(c.Bucket)})
	return err
}
func (s *Server) updateConnection(w http.ResponseWriter, r *http.Request) {
	session, _, data, err := s.unlocked(r)
	if err != nil {
		fail(w, 423, err)
		return
	}
	var update vault.Connection
	if json.NewDecoder(r.Body).Decode(&update) != nil || update.ID == "" || update.Name == "" || update.Bucket == "" {
		fail(w, 400, errors.New("id, name, and bucket are required"))
		return
	}
	if s.isOrganizationConnection(update.ID) {
		fail(w, http.StatusForbidden, errors.New("organization connections cannot be edited"))
		return
	}
	for i := range data.Connections {
		if data.Connections[i].ID != update.ID {
			continue
		}
		if update.AccessKey == "" {
			update.AccessKey = data.Connections[i].AccessKey
		}
		if update.SecretKey == "" {
			update.SecretKey = data.Connections[i].SecretKey
		}
		if err := validateS3Connection(r.Context(), update); err != nil {
			fail(w, http.StatusBadRequest, fmt.Errorf("connection test failed: %w", err))
			return
		}
		data.Connections[i] = update
		if err = s.Vault.Save(session.User.ID, session.Password, data); err != nil {
			fail(w, 500, err)
			return
		}
		write(w, map[string]bool{"ok": true})
		s.audit(session, "connection.update", "success", map[string]string{"connection_id": update.ID, "bucket": update.Bucket})
		return
	}
	fail(w, 404, errors.New("connection not found"))
}
func (s *Server) deleteConnection(w http.ResponseWriter, r *http.Request) {
	session, _, data, err := s.unlocked(r)
	if err != nil {
		fail(w, 423, err)
		return
	}
	id := r.URL.Query().Get("id")
	if s.isOrganizationConnection(id) {
		fail(w, http.StatusForbidden, errors.New("organization connections cannot be deleted"))
		return
	}
	for i, c := range data.Connections {
		if c.ID == id {
			data.Connections = append(data.Connections[:i], data.Connections[i+1:]...)
			break
		}
	}
	if err = s.Vault.Save(session.User.ID, session.Password, data); err != nil {
		fail(w, 500, err)
		return
	}
	write(w, map[string]bool{"ok": true})
	s.audit(session, "connection.delete", "success", map[string]string{"connection_id": id})
}
func (s *Server) isOrganizationConnection(id string) bool {
	for _, org := range s.organizationConfig() {
		for _, connection := range org.Connections {
			if org.ID+":"+connection.ID == id {
				return true
			}
		}
	}
	return false
}
func (s *Server) createFolder(w http.ResponseWriter, r *http.Request) {
	session, _, data, err := s.unlocked(r)
	if err != nil {
		fail(w, 423, err)
		return
	}
	c, err := s.connection(r, data, session.User)
	if err != nil {
		fail(w, 404, err)
		return
	}
	var request struct{ Name, Prefix string }
	if json.NewDecoder(r.Body).Decode(&request) != nil || strings.TrimSpace(request.Name) == "" {
		fail(w, 400, errors.New("folder name is required"))
		return
	}
	name := strings.Trim(strings.TrimSpace(request.Name), "/")
	prefix := strings.Trim(request.Prefix, "/")
	key := name + "/"
	if prefix != "" {
		key = prefix + "/" + key
	}
	client, err := s3client.New(r.Context(), c)
	if err != nil {
		fail(w, 500, err)
		return
	}
	if _, err = client.PutObject(r.Context(), &s3.PutObjectInput{Bucket: aws.String(c.Bucket), Key: aws.String(key), Body: strings.NewReader("")}); err != nil {
		fail(w, 502, err)
		return
	}
	write(w, map[string]string{"key": key})
	s.audit(session, "folder.create", "success", map[string]string{"connection_id": c.ID, "bucket": c.Bucket, "object_key": key})
}
func (s *Server) connection(r *http.Request, data vault.Data, user auth.User) (vault.Connection, error) {
	return s.connectionValue(r.URL.Query().Get("connection"), data, user)
}
func (s *Server) connectionValue(id string, data vault.Data, user auth.User) (vault.Connection, error) {
	connections := s.withOrganizations(user, data).Connections
	for _, c := range connections {
		if c.ID == id {
			return c, nil
		}
	}
	return vault.Connection{}, errors.New("connection not found")
}
func (s *Server) withOrganizations(user auth.User, data vault.Data) vault.Data {
	result := vault.Data{Connections: append([]vault.Connection(nil), data.Connections...)}
	organizations := s.organizationConfig()
	organizationNames := make([]string, 0, len(organizations))
	for name := range organizations {
		organizationNames = append(organizationNames, name)
	}
	sort.Strings(organizationNames)
	for _, organizationName := range organizationNames {
		org := organizations[organizationName]
		if !hasGroup(user.Groups, org.Groups) {
			continue
		}
		connectionNames := make([]string, 0, len(org.Connections))
		for name := range org.Connections {
			connectionNames = append(connectionNames, name)
		}
		sort.Strings(connectionNames)
		for _, connectionName := range connectionNames {
			configured := org.Connections[connectionName]
			c := configured.Connection
			if c.ID == "" {
				c.ID = connectionName
			}
			if c.Name == "" {
				c.Name = connectionName
			}
			c.ID = org.ID + ":" + c.ID
			if org.Name != "" {
				c.Name = org.Name + " / " + c.Name
			}
			result.Connections = append(result.Connections, c)
		}
	}
	return result
}
func (s *Server) organizationConfig() map[string]vault.Organization {
	result := make(map[string]vault.Organization, len(s.Organizations))
	for name, organization := range s.Organizations {
		result[name] = organization
	}
	if dynamic, err := s.Vault.ListOrganizations(); err == nil {
		for name, organization := range dynamic {
			result[name] = organization
		}
	}
	return result
}
func hasGroup(userGroups, required []string) bool {
	for _, userGroup := range userGroups {
		for _, group := range required {
			if userGroup == group {
				return true
			}
		}
	}
	return false
}

type item struct {
	Name     string     `json:"name"`
	Key      string     `json:"key"`
	Kind     string     `json:"kind"`
	Size     int64      `json:"size,omitempty"`
	Modified *time.Time `json:"modified,omitempty"`
}

func (s *Server) browse(w http.ResponseWriter, r *http.Request) {
	session, _, data, err := s.unlocked(r)
	if err != nil {
		fail(w, 423, err)
		return
	}
	c, err := s.connection(r, data, session.User)
	if err != nil {
		fail(w, 404, err)
		return
	}
	client, err := s3client.New(r.Context(), c)
	if err != nil {
		fail(w, 500, err)
		return
	}
	prefix := r.URL.Query().Get("prefix")
	if prefix == "" {
		prefix = c.Prefix
	}
	kind := r.URL.Query().Get("kind")
	if kind == "" {
		kind = "all"
	}
	if kind != "all" && kind != "file" && kind != "folder" {
		fail(w, http.StatusBadRequest, fmt.Errorf("invalid browse kind %q", kind))
		return
	}
	input := &s3.ListObjectsV2Input{
		Bucket:    aws.String(c.Bucket),
		Prefix:    aws.String(prefix),
		Delimiter: aws.String("/"),
	}
	if token := r.URL.Query().Get("continuationToken"); token != "" {
		input.ContinuationToken = aws.String(token)
	}
	result, err := client.ListObjectsV2(r.Context(), input)
	if err != nil {
		fail(w, 502, err)
		return
	}
	items := make([]item, 0)
	if kind != "file" {
		for _, p := range result.CommonPrefixes {
			key := aws.ToString(p.Prefix)
			items = append(items, item{Name: strings.TrimSuffix(strings.TrimPrefix(key, prefix), "/"), Key: key, Kind: "folder"})
		}
	}
	if kind != "folder" {
		for _, object := range result.Contents {
			key := aws.ToString(object.Key)
			if key != prefix {
				items = append(items, item{Name: path.Base(key), Key: key, Kind: "file", Size: aws.ToInt64(object.Size), Modified: object.LastModified})
			}
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Kind > items[j].Kind || items[i].Name < items[j].Name })
	write(w, map[string]any{
		"connection": c.Name,
		"prefix":     prefix,
		"items":      items,
		"nextToken":  aws.ToString(result.NextContinuationToken),
		"hasMore":    aws.ToBool(result.IsTruncated),
	})
}
func (s *Server) file(w http.ResponseWriter, r *http.Request) {
	session, _, data, err := s.unlocked(r)
	if err != nil {
		fail(w, 423, err)
		return
	}
	c, err := s.connection(r, data, session.User)
	if err != nil {
		fail(w, 404, err)
		return
	}
	client, err := s3client.New(r.Context(), c)
	if err != nil {
		fail(w, 500, err)
		return
	}
	object, err := client.GetObject(r.Context(), &s3.GetObjectInput{Bucket: aws.String(c.Bucket), Key: aws.String(r.URL.Query().Get("key"))})
	if err != nil {
		fail(w, 502, err)
		return
	}
	defer object.Body.Close()
	if object.ContentType != nil {
		w.Header().Set("Content-Type", aws.ToString(object.ContentType))
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", path.Base(r.URL.Query().Get("key"))))
	_, _ = io.Copy(w, object.Body)
}
func (s *Server) deleteFile(w http.ResponseWriter, r *http.Request) {
	session, _, data, err := s.unlocked(r)
	if err != nil {
		fail(w, 423, err)
		return
	}
	c, err := s.connection(r, data, session.User)
	if err != nil {
		fail(w, 404, err)
		return
	}
	client, err := s3client.New(r.Context(), c)
	if err != nil {
		fail(w, 500, err)
		return
	}
	key := r.URL.Query().Get("key")
	digest := ""
	if strings.HasSuffix(key, "/") {
		var token *string
		for {
			list, listErr := client.ListObjectsV2(r.Context(), &s3.ListObjectsV2Input{Bucket: aws.String(c.Bucket), Prefix: aws.String(key), ContinuationToken: token})
			if listErr != nil {
				fail(w, 502, listErr)
				return
			}
			objects := make([]types.ObjectIdentifier, 0, len(list.Contents))
			for _, object := range list.Contents {
				objects = append(objects, types.ObjectIdentifier{Key: object.Key})
			}
			if len(objects) > 0 {
				if _, err = client.DeleteObjects(r.Context(), &s3.DeleteObjectsInput{Bucket: aws.String(c.Bucket), Delete: &types.Delete{Objects: objects, Quiet: aws.Bool(true)}}); err != nil {
					fail(w, 502, err)
					return
				}
			}
			if !aws.ToBool(list.IsTruncated) {
				break
			}
			token = list.NextContinuationToken
		}
	} else {
		digest, err = objectDigest(r.Context(), client, c.Bucket, key)
		if err != nil {
			fail(w, 502, err)
			return
		}
		_, err = client.DeleteObject(r.Context(), &s3.DeleteObjectInput{Bucket: aws.String(c.Bucket), Key: aws.String(key)})
	}
	if err != nil {
		fail(w, 502, err)
		return
	}
	write(w, map[string]bool{"ok": true})
	s.audit(session, "object.delete", "success", map[string]string{"connection_id": c.ID, "bucket": c.Bucket, "object_key": key, "file_sha256": digest})
}
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	session, _, data, err := s.unlocked(r)
	if err != nil {
		fail(w, 423, err)
		return
	}
	c, err := s.connection(r, data, session.User)
	if err != nil {
		fail(w, 404, err)
		return
	}
	if err = r.ParseMultipartForm(32 << 20); err != nil {
		fail(w, 400, err)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		fail(w, 400, errors.New("file is required"))
		return
	}
	defer file.Close()
	client, err := s3client.New(r.Context(), c)
	if err != nil {
		fail(w, 500, err)
		return
	}
	hasher := sha256.New()
	key := r.URL.Query().Get("prefix") + header.Filename
	if _, err = io.Copy(hasher, file); err != nil {
		s.audit(session, "object.upload", "error", map[string]string{"connection_id": c.ID, "bucket": c.Bucket, "object_key": key, "file_sha256": hex.EncodeToString(hasher.Sum(nil)), "error": err.Error()})
		fail(w, 502, err)
		return
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		fail(w, 500, err)
		return
	}
	checksum := base64.StdEncoding.EncodeToString(hasher.Sum(nil))
	if header.Size > 5*1024*1024*1024 {
		fail(w, 413, errors.New("file exceeds the S3 single-upload limit"))
		return
	}
	if header.Size > 100*1024*1024 {
		err = multipartUpload(r.Context(), client, c.Bucket, key, file, header.Size, header.Header.Get("Content-Type"))
	} else {
		_, err = client.PutObject(r.Context(), &s3.PutObjectInput{Bucket: aws.String(c.Bucket), Key: aws.String(key), Body: file, ContentLength: aws.Int64(header.Size), ContentType: aws.String(header.Header.Get("Content-Type")), ChecksumSHA256: aws.String(checksum)})
	}
	if err != nil {
		s.audit(session, "object.upload", "error", map[string]string{"connection_id": c.ID, "bucket": c.Bucket, "object_key": key, "file_sha256": hex.EncodeToString(hasher.Sum(nil)), "error": err.Error()})
		fail(w, 502, err)
		return
	}
	write(w, map[string]bool{"ok": true})
	s.audit(session, "object.upload", "success", map[string]string{"connection_id": c.ID, "bucket": c.Bucket, "object_key": key, "file_sha256": hex.EncodeToString(hasher.Sum(nil))})
}

func (s *Server) uploadInit(w http.ResponseWriter, r *http.Request) {
	session, _, data, err := s.unlocked(r)
	if err != nil {
		fail(w, 423, err)
		return
	}
	var request struct {
		Connection  string `json:"connection"`
		Prefix      string `json:"prefix"`
		Name        string `json:"name"`
		Size        int64  `json:"size"`
		ContentType string `json:"contentType"`
	}
	if err = json.NewDecoder(r.Body).Decode(&request); err != nil || strings.TrimSpace(request.Name) == "" || request.Size < 0 {
		fail(w, http.StatusBadRequest, errors.New("file name and size are required"))
		return
	}
	c, err := s.connectionValue(request.Connection, data, session.User)
	if err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}
	client, err := s3client.New(r.Context(), c)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	key := request.Prefix + request.Name
	created, err := client.CreateMultipartUpload(r.Context(), &s3.CreateMultipartUploadInput{Bucket: aws.String(c.Bucket), Key: aws.String(key), ContentType: aws.String(request.ContentType)})
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	if aws.ToString(created.UploadId) == "" {
		fail(w, http.StatusBadGateway, errors.New("S3 returned an empty multipart upload ID"))
		return
	}
	s.uploadMu.Lock()
	if s.uploads == nil {
		s.uploads = make(map[string]*uploadState)
	}
	s.uploads[aws.ToString(created.UploadId)] = &uploadState{UserID: session.User.ID, Connection: c, Bucket: c.Bucket, Key: key, UploadID: aws.ToString(created.UploadId), Hash: sha256.New(), NextPart: 1}
	s.uploadMu.Unlock()
	write(w, map[string]string{"uploadId": aws.ToString(created.UploadId), "key": key})
}

func (s *Server) uploadPart(w http.ResponseWriter, r *http.Request) {
	session, _, data, err := s.unlocked(r)
	if err != nil {
		fail(w, 423, err)
		return
	}
	uploadID := r.URL.Query().Get("uploadId")
	partNumber, err := strconv.ParseInt(r.URL.Query().Get("partNumber"), 10, 32)
	if uploadID == "" || err != nil || partNumber < 1 {
		fail(w, http.StatusBadRequest, errors.New("upload ID and part number are required"))
		return
	}
	s.uploadMu.Lock()
	state, ok := s.uploads[uploadID]
	s.uploadMu.Unlock()
	if !ok || state.UserID != session.User.ID {
		fail(w, http.StatusNotFound, errors.New("multipart upload not found"))
		return
	}
	if int32(partNumber) != state.NextPart {
		fail(w, http.StatusBadRequest, errors.New("multipart parts must be uploaded in order"))
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, multipartPartSize+1))
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if int64(len(body)) > multipartPartSize {
		fail(w, http.StatusRequestEntityTooLarge, errors.New("upload part is too large"))
		return
	}
	c, err := s.connectionValue(r.URL.Query().Get("connection"), data, session.User)
	if err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}
	client, err := s3client.New(r.Context(), c)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	part, err := client.UploadPart(r.Context(), &s3.UploadPartInput{Bucket: aws.String(state.Bucket), Key: aws.String(state.Key), UploadId: aws.String(uploadID), PartNumber: aws.Int32(int32(partNumber)), Body: bytes.NewReader(body), ContentLength: aws.Int64(int64(len(body)))})
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	s.uploadMu.Lock()
	state.Hash.Write(body)
	state.Parts = append(state.Parts, types.CompletedPart{ETag: part.ETag, PartNumber: aws.Int32(int32(partNumber))})
	state.NextPart++
	s.uploadMu.Unlock()
	write(w, map[string]any{"partNumber": partNumber, "etag": aws.ToString(part.ETag)})
}

func (s *Server) uploadComplete(w http.ResponseWriter, r *http.Request) {
	session, _, data, err := s.unlocked(r)
	if err != nil {
		fail(w, 423, err)
		return
	}
	uploadID := r.URL.Query().Get("uploadId")
	if uploadID == "" {
		fail(w, http.StatusBadRequest, errors.New("upload ID is required"))
		return
	}
	s.uploadMu.Lock()
	state, ok := s.uploads[uploadID]
	if ok {
		delete(s.uploads, uploadID)
	}
	s.uploadMu.Unlock()
	if !ok || state.UserID != session.User.ID || len(state.Parts) == 0 {
		fail(w, http.StatusNotFound, errors.New("multipart upload not found or empty"))
		return
	}
	c, err := s.connectionValue(r.URL.Query().Get("connection"), data, session.User)
	if err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}
	client, err := s3client.New(r.Context(), c)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	_, err = client.CompleteMultipartUpload(r.Context(), &s3.CompleteMultipartUploadInput{Bucket: aws.String(state.Bucket), Key: aws.String(state.Key), UploadId: aws.String(uploadID), MultipartUpload: &types.CompletedMultipartUpload{Parts: state.Parts}})
	if err != nil {
		_, _ = client.AbortMultipartUpload(r.Context(), &s3.AbortMultipartUploadInput{Bucket: aws.String(state.Bucket), Key: aws.String(state.Key), UploadId: aws.String(uploadID)})
		fail(w, http.StatusBadGateway, err)
		return
	}
	digest := hex.EncodeToString(state.Hash.Sum(nil))
	s.audit(session, "object.upload", "success", map[string]string{"connection_id": c.ID, "bucket": c.Bucket, "object_key": state.Key, "file_sha256": digest})
	write(w, map[string]bool{"ok": true})
}

func (s *Server) uploadAbort(w http.ResponseWriter, r *http.Request) {
	session, _, _, err := s.unlocked(r)
	if err != nil {
		fail(w, 423, err)
		return
	}
	uploadID := r.URL.Query().Get("uploadId")
	s.uploadMu.Lock()
	state, ok := s.uploads[uploadID]
	if ok {
		delete(s.uploads, uploadID)
	}
	s.uploadMu.Unlock()
	if !ok || state.UserID != session.User.ID {
		write(w, map[string]bool{"ok": true})
		return
	}
	client, err := s3client.New(r.Context(), state.Connection)
	if err == nil {
		_, _ = client.AbortMultipartUpload(r.Context(), &s3.AbortMultipartUploadInput{Bucket: aws.String(state.Bucket), Key: aws.String(state.Key), UploadId: aws.String(uploadID)})
	}
	write(w, map[string]bool{"ok": true})
}

const multipartPartSize int64 = 64 * 1024 * 1024

func multipartUpload(ctx context.Context, client *s3.Client, bucket, key string, file io.ReaderAt, size int64, contentType string) error {
	created, err := client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{Bucket: aws.String(bucket), Key: aws.String(key), ContentType: aws.String(contentType)})
	if err != nil {
		return err
	}
	uploadID := aws.ToString(created.UploadId)
	abort := true
	defer func() {
		if abort {
			_, _ = client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{Bucket: aws.String(bucket), Key: aws.String(key), UploadId: aws.String(uploadID)})
		}
	}()
	parts := make([]types.CompletedPart, 0, (size+multipartPartSize-1)/multipartPartSize)
	for partNumber, offset := int32(1), int64(0); offset < size; partNumber, offset = partNumber+1, offset+multipartPartSize {
		partSize := multipartPartSize
		if remaining := size - offset; remaining < partSize {
			partSize = remaining
		}
		part, err := client.UploadPart(ctx, &s3.UploadPartInput{Bucket: aws.String(bucket), Key: aws.String(key), UploadId: aws.String(uploadID), PartNumber: aws.Int32(partNumber), Body: io.NewSectionReader(file, offset, partSize), ContentLength: aws.Int64(partSize)})
		if err != nil {
			return err
		}
		parts = append(parts, types.CompletedPart{ETag: part.ETag, PartNumber: aws.Int32(partNumber)})
	}
	_, err = client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{Bucket: aws.String(bucket), Key: aws.String(key), UploadId: aws.String(uploadID), MultipartUpload: &types.CompletedMultipartUpload{Parts: parts}})
	if err == nil {
		abort = false
	}
	return err
}

func objectDigest(ctx context.Context, client *s3.Client, bucket, key string) (string, error) {
	object, err := client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), ChecksumMode: types.ChecksumModeEnabled})
	if err != nil {
		return "", err
	}
	if object.ChecksumSHA256 == nil || aws.ToString(object.ChecksumSHA256) == "" {
		return "", nil
	}
	digest, err := base64.StdEncoding.DecodeString(aws.ToString(object.ChecksumSHA256))
	if err != nil {
		return "", nil
	}
	return hex.EncodeToString(digest), nil
}
func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	session, _, data, err := s.unlocked(r)
	if err != nil {
		fail(w, 423, err)
		return
	}
	c, err := s.connection(r, data, session.User)
	if err != nil {
		fail(w, 404, err)
		return
	}
	prefix, format := r.URL.Query().Get("prefix"), r.URL.Query().Get("format")
	if format != "zip" && format != "tgz" {
		fail(w, 400, errors.New("format must be zip or tgz"))
		return
	}
	client, err := s3client.New(r.Context(), c)
	if err != nil {
		fail(w, 500, err)
		return
	}
	name := strings.Trim(strings.TrimSuffix(prefix, "/"), "/")
	if name == "" {
		name = c.Bucket
	}
	ext := "." + format
	if format == "tgz" {
		ext = ".tar.gz"
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name+ext))
	var zw *zip.Writer
	var tw *tar.Writer
	var gz *gzip.Writer
	if format == "zip" {
		zw = zip.NewWriter(w)
		defer zw.Close()
	} else {
		gz = gzip.NewWriter(w)
		defer gz.Close()
		tw = tar.NewWriter(gz)
		defer tw.Close()
	}
	p := &s3.ListObjectsV2Input{Bucket: aws.String(c.Bucket), Prefix: aws.String(prefix)}
	for {
		out, e := client.ListObjectsV2(r.Context(), p)
		if e != nil {
			return
		}
		for _, obj := range out.Contents {
			key := aws.ToString(obj.Key)
			rel := strings.TrimPrefix(key, prefix)
			if rel == "" {
				continue
			}
			body, e := client.GetObject(r.Context(), &s3.GetObjectInput{Bucket: aws.String(c.Bucket), Key: aws.String(key)})
			if e != nil {
				return
			}
			if format == "zip" {
				entry, e := zw.Create(rel)
				if e == nil {
					_, e = io.Copy(entry, body.Body)
				}
				body.Body.Close()
				if e != nil {
					return
				}
			} else {
				header := &tar.Header{Name: rel, Mode: 0600, Size: aws.ToInt64(obj.Size), ModTime: aws.ToTime(obj.LastModified)}
				if e = tw.WriteHeader(header); e == nil {
					_, e = io.Copy(tw, body.Body)
				}
				body.Body.Close()
				if e != nil {
					return
				}
			}
		}
		if !aws.ToBool(out.IsTruncated) {
			break
		}
		p.ContinuationToken = out.NextContinuationToken
	}
	s.audit(session, "object.download", "success", map[string]string{"connection_id": c.ID, "bucket": c.Bucket, "object_key": prefix})
}
func publicConnections(data vault.Data) []vault.Connection {
	result := make([]vault.Connection, 0, len(data.Connections))
	for _, c := range data.Connections {
		c.AccessKey, c.SecretKey = "", ""
		result = append(result, c)
	}
	return result
}
func randomID() string { return fmt.Sprintf("%d", time.Now().UnixNano()) }
func write(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, status int, err error) { http.Error(w, err.Error(), status) }
