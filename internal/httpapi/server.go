package httpapi

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"mariner/internal/audit"
	"mariner/internal/auth"
	"mariner/internal/config"
	connectioncontroller "mariner/internal/httpapi/controller"
	"mariner/internal/httpapi/mapper"
	"mariner/internal/model"
	"mariner/internal/repository"
	s3service "mariner/internal/service/s3"
	"mariner/internal/vault"
)

type Server struct {
	Auth                    *auth.Service
	Vault                   *vault.Store
	AuditRepo               repository.AuditEventRepository
	OrganizationRepo        repository.OrganizationRepository
	PreferenceRepo          repository.UserPreferenceRepository
	ApplicationSettingsRepo repository.ApplicationSettingsRepository
	Storage                 *s3service.Service
	Audit                   *audit.Logger
	AuditAdminGroup         string
	Organizations           map[string]config.Organization
	Connections             connectioncontroller.ConnectionUseCase
	ConnectionCtrl          *connectioncontroller.ConnectionController
	uploadMu                sync.Mutex
	uploads                 map[string]*uploadState
	limiter                 *rateLimiter
	RateLimitEnabled        bool
	RateLimitWindow         time.Duration
	LoginRateLimit          int
	UnlockRateLimit         int
	ConnectionTestRateLimit int
	UploadPartSize          int64
	UploadPartConcurrency   int
}

type uploadState struct {
	UserID     string
	Connection vault.Connection
	Bucket     string
	Key        string
	UploadID   string
	Parts      map[int32]model.UploadPart
	PartPaths  map[int32]string
	TempDir    string
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
		slog.Error("audit write failed", "action", action, "error", err)
	}
}

func (s *Server) auditRepository() repository.AuditEventRepository {
	if s.AuditRepo != nil {
		return s.AuditRepo
	}
	return s.Vault.AuditRepository()
}

func (s *Server) organizationRepository() repository.OrganizationRepository {
	if s.OrganizationRepo != nil {
		return s.OrganizationRepo
	}
	return s.Vault.OrganizationRepository()
}

func (s *Server) preferenceRepository() repository.UserPreferenceRepository {
	if s.PreferenceRepo != nil {
		return s.PreferenceRepo
	}
	return s.Vault.UserPreferenceRepository()
}

func (s *Server) Router() http.Handler {
	if s.limiter == nil {
		s.limiter = &rateLimiter{entries: map[string]rateWindow{}}
	}
	r := chi.NewRouter()
	r.Use(s.securityMiddleware)
	r.Use(s.sensitiveRateLimit)
	r.Get("/auth/login", s.login)
	r.Get("/auth/callback", s.callback)
	r.Post("/auth/logout", s.logout)
	r.Get("/api/healthz", s.healthz)
	r.Get("/api/me", s.me)
	r.Get("/api/admin/audit", s.adminAudit)
	r.Get("/api/admin/audit/actions", s.adminAuditActions)
	r.Get("/api/admin/preferences", s.adminPreferences)
	r.Put("/api/admin/preferences", s.updateAdminPreferences)
	r.Get("/api/admin/settings", s.adminSettings)
	r.Put("/api/admin/settings", s.updateAdminSettings)
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
	r.Get("/api/connections", s.connectionController().List)
	r.Post("/api/connections", s.connectionController().Create)
	r.Post("/api/connections/test", s.connectionController().Test)
	r.Put("/api/connections", s.connectionController().Update)
	r.Delete("/api/connections", s.connectionController().Delete)
	r.Post("/api/folders", s.createFolder)
	r.Get("/api/browse", s.browse)
	r.Get("/api/search", s.search)
	r.Get("/api/multipart", s.multipartUploads)
	r.Delete("/api/multipart", s.multipartAbort)
	r.Get("/api/versions", s.versions)
	r.Delete("/api/versions", s.deleteVersion)
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
	page, err := s.auditRepository().List(vault.AuditFilter{
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
	actions, err := s.auditRepository().ListActions()
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
	preferences, err := s.preferenceRepository().Get(session.User.ID)
	if err != nil {
		fail(w, 500, err)
		return
	}
	write(w, preferences)
}

func (s *Server) applicationSettingsRepository() repository.ApplicationSettingsRepository {
	if s.ApplicationSettingsRepo != nil {
		return s.ApplicationSettingsRepo
	}
	return s.Vault.ApplicationSettingsRepository()
}

func (s *Server) adminSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.adminAllowed(r); !ok {
		fail(w, http.StatusForbidden, errors.New("administrator access required"))
		return
	}
	settings, err := s.applicationSettingsRepository().Get()
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	write(w, settings)
}

func (s *Server) updateAdminSettings(w http.ResponseWriter, r *http.Request) {
	session, ok := s.adminAllowed(r)
	if !ok {
		fail(w, http.StatusForbidden, errors.New("administrator access required"))
		return
	}
	var settings model.ApplicationSettings
	if err := DecodeJSON(w, r, &settings); err != nil || settings.ArchiveMaxBytes <= 0 {
		fail(w, http.StatusBadRequest, errors.New("archive max bytes must be positive"))
		return
	}
	if err := s.applicationSettingsRepository().Save(settings); err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	s.audit(session, "application-settings.update", "success", nil)
	write(w, settings)
}
func (s *Server) updateAdminPreferences(w http.ResponseWriter, r *http.Request) {
	session, ok := s.adminAllowed(r)
	if !ok {
		fail(w, 403, errors.New("administrator access required"))
		return
	}
	preferences := map[string]bool{}
	if DecodeJSON(w, r, &preferences) != nil {
		fail(w, 400, errors.New("invalid preferences"))
		return
	}
	if err := s.preferenceRepository().Save(session.User.ID, preferences); err != nil {
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
	organizations, err := s.organizationRepository().List()
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
	if DecodeJSON(w, r, &organization) != nil || organization.ID == "" || organization.Name == "" {
		fail(w, 400, errors.New("organization id and name are required"))
		return
	}
	if _, provisioned := s.Organizations[organization.ID]; provisioned {
		fail(w, 409, errors.New("Helm-provisioned organizations are read-only"))
		return
	}
	if update {
		stored, err := s.organizationRepository().List()
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
	if err := s.organizationRepository().Save(organization); err != nil {
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
	if err := s.organizationRepository().Delete(id); err != nil {
		slog.Error("admin organization database delete failed", "error", err)
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
		slog.Warn("authentication failure: OIDC callback rejected", "error", err)
		fail(w, http.StatusUnauthorized, errors.New("authentication failed"))
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
func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	write(w, map[string]string{"status": "ok"})
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
	if DecodeJSON(w, r, &request) != nil || len(request.Password) < 10 {
		fail(w, 400, errors.New("master password must be at least 10 characters"))
		return
	}
	data, exists, err := s.Vault.Load(session.User.ID, request.Password)
	if err != nil {
		s.audit(session, "vault.unlock", "error", map[string]string{"error": "invalid master password"})
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
	if DecodeJSON(w, r, &settings) != nil || (settings.Theme != "light" && settings.Theme != "dark") {
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

// IsOrganizationConnection exposes the policy callback used by the
// connection API while keeping organization configuration owned by the HTTP
// server during this incremental migration.
func (s *Server) IsOrganizationConnection(id string) bool {
	return s.isOrganizationConnection(id)
}

func (s *Server) connectionController() *connectioncontroller.ConnectionController {
	if s.ConnectionCtrl == nil {
		s.ConnectionCtrl = &connectioncontroller.ConnectionController{
			API: s.Connections,
			Unlocked: func(r *http.Request) (auth.Session, error) {
				session, _, _, err := s.unlocked(r)
				return session, err
			},
			AdminAllowed: s.adminAllowed,
			MergeConnections: func(user auth.User, connections []model.Connection) []model.Connection {
				merged := s.withOrganizations(user, vault.Data{Connections: connections})
				return merged.Connections
			},
			Audit: s.audit,
		}
	}
	return s.ConnectionCtrl
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
	if DecodeJSON(w, r, &request) != nil || strings.TrimSpace(request.Name) == "" {
		fail(w, 400, errors.New("folder name is required"))
		return
	}
	name := strings.Trim(strings.TrimSpace(request.Name), "/")
	prefix := strings.Trim(request.Prefix, "/")
	if err := validateObjectName(prefix + "/" + name); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	key := name + "/"
	if prefix != "" {
		key = prefix + "/" + key
	}
	if s.Storage == nil {
		fail(w, http.StatusInternalServerError, errors.New("S3 service is not configured"))
		return
	}
	if _, err = s.Storage.CreateFolder(r.Context(), c, prefix, name); err != nil {
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
	if dynamic, err := s.organizationRepository().List(); err == nil {
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
	prefix := r.URL.Query().Get("prefix")
	kind := r.URL.Query().Get("kind")
	if s.Storage == nil {
		fail(w, http.StatusInternalServerError, errors.New("S3 service is not configured"))
		return
	}
	result, err := s.Storage.Browse(r.Context(), c, prefix, kind, r.URL.Query().Get("continuationToken"))
	if err != nil {
		if strings.Contains(err.Error(), "invalid browse kind") {
			fail(w, http.StatusBadRequest, err)
		} else {
			fail(w, 502, err)
		}
		return
	}
	write(w, map[string]any{
		"connection": result.Connection,
		"prefix":     result.Prefix,
		"items":      result.Items,
		"nextToken":  result.NextToken,
		"hasMore":    result.HasMore,
	})
}

// search scans the current S3 prefix one level deep. The search is performed
// by S3 rather than against the currently loaded browser page, so it also
// finds matches beyond the normal ListObjectsV2 page boundary.
func (s *Server) search(w http.ResponseWriter, r *http.Request) {
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
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	if query == "" {
		fail(w, http.StatusBadRequest, fmt.Errorf("search query is required"))
		return
	}
	kind := r.URL.Query().Get("kind")
	if kind == "" {
		kind = "all"
	}
	if kind != "all" && kind != "file" && kind != "folder" {
		fail(w, http.StatusBadRequest, fmt.Errorf("invalid search kind %q", kind))
		return
	}
	if s.Storage == nil {
		fail(w, http.StatusInternalServerError, errors.New("S3 service is not configured"))
		return
	}
	prefix := r.URL.Query().Get("prefix")
	result, err := s.Storage.Search(r.Context(), c, prefix, query, kind)
	if err != nil {
		if strings.Contains(err.Error(), "required") || strings.Contains(err.Error(), "invalid browse kind") {
			fail(w, http.StatusBadRequest, err)
		} else {
			fail(w, 502, err)
		}
		return
	}
	write(w, map[string]any{
		"connection": result.Connection,
		"prefix":     result.Prefix,
		"items":      result.Items,
		"hasMore":    false,
	})
}

func (s *Server) versions(w http.ResponseWriter, r *http.Request) {
	session, _, data, err := s.unlocked(r)
	if err != nil {
		fail(w, http.StatusLocked, err)
		return
	}
	key := r.URL.Query().Get("key")
	if key == "" {
		fail(w, http.StatusBadRequest, errors.New("object key is required"))
		return
	}
	c, err := s.connection(r, data, session.User)
	if err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}
	if s.Storage == nil {
		fail(w, http.StatusInternalServerError, errors.New("S3 service is not configured"))
		return
	}
	versions, err := s.Storage.Versions(r.Context(), c, key)
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	write(w, map[string]any{"versions": versions})
}

func (s *Server) deleteVersion(w http.ResponseWriter, r *http.Request) {
	session, _, data, err := s.unlocked(r)
	if err != nil {
		fail(w, http.StatusLocked, err)
		return
	}
	key := r.URL.Query().Get("key")
	versionID := r.URL.Query().Get("versionId")
	if key == "" || versionID == "" {
		fail(w, http.StatusBadRequest, errors.New("object key and version ID are required"))
		return
	}
	c, err := s.connection(r, data, session.User)
	if err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}
	if s.Storage == nil {
		fail(w, http.StatusInternalServerError, errors.New("S3 service is not configured"))
		return
	}
	if err = s.Storage.DeleteVersion(r.Context(), c, key, versionID); err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	s.audit(session, "object.version_delete", "success", map[string]string{"connection_id": c.ID, "bucket": c.Bucket, "object_key": key})
	write(w, map[string]bool{"ok": true})
}

func (s *Server) multipartUploads(w http.ResponseWriter, r *http.Request) {
	session, _, data, err := s.unlocked(r)
	if err != nil {
		fail(w, http.StatusLocked, err)
		return
	}
	c, err := s.connection(r, data, session.User)
	if err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}
	if s.Storage == nil {
		fail(w, http.StatusInternalServerError, errors.New("S3 service is not configured"))
		return
	}
	uploads, err := s.Storage.MultipartUploads(r.Context(), c, r.URL.Query().Get("prefix"))
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	write(w, map[string]any{"uploads": uploads})
}

func (s *Server) multipartAbort(w http.ResponseWriter, r *http.Request) {
	session, _, data, err := s.unlocked(r)
	if err != nil {
		fail(w, http.StatusLocked, err)
		return
	}
	uploadID := r.URL.Query().Get("uploadId")
	if uploadID == "" {
		fail(w, http.StatusBadRequest, errors.New("upload ID is required"))
		return
	}
	c, err := s.connection(r, data, session.User)
	if err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}
	if s.Storage == nil {
		fail(w, http.StatusInternalServerError, errors.New("S3 service is not configured"))
		return
	}
	if err = s.Storage.AbortMultipart(r.Context(), c, uploadID, r.URL.Query().Get("key")); err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	s.audit(session, "multipart.abort", "success", map[string]string{"connection_id": c.ID, "bucket": c.Bucket, "object_key": r.URL.Query().Get("key")})
	write(w, map[string]bool{"ok": true})
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
	if s.Storage == nil {
		fail(w, http.StatusInternalServerError, errors.New("S3 service is not configured"))
		return
	}
	key := r.URL.Query().Get("key")
	object, err := s.Storage.OpenObject(r.Context(), c, key, r.URL.Query().Get("versionId"))
	if err != nil {
		fail(w, 502, err)
		return
	}
	defer object.Body.Close()
	if object.ContentType != "" {
		w.Header().Set("Content-Type", object.ContentType)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", safeDownloadName(path.Base(key))))
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
	if s.Storage == nil {
		fail(w, http.StatusInternalServerError, errors.New("S3 service is not configured"))
		return
	}
	key := r.URL.Query().Get("key")
	digest, err := s.Storage.DeleteFile(r.Context(), c, key)
	if err != nil {
		fail(w, 502, err)
		return
	}
	write(w, map[string]bool{"ok": true})
	s.audit(session, "object.delete", "success", map[string]string{"connection_id": c.ID, "bucket": c.Bucket, "object_key": key, "file_sha256": digest})
}
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 5*1024*1024*1024+32<<20)
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
	if s.Storage == nil {
		fail(w, http.StatusInternalServerError, errors.New("S3 service is not configured"))
		return
	}
	hasher := sha256.New()
	key := r.URL.Query().Get("prefix") + header.Filename
	if err := validateObjectName(key); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
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
	err = s.Storage.Upload(r.Context(), c, key, file, header.Size, header.Header.Get("Content-Type"), checksum)
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
	if err = DecodeJSON(w, r, &request); err != nil || strings.TrimSpace(request.Name) == "" || request.Size < 0 {
		fail(w, http.StatusBadRequest, errors.New("file name and size are required"))
		return
	}
	if err := validateObjectName(request.Prefix + request.Name); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	c, err := s.connectionValue(request.Connection, data, session.User)
	if err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}
	if s.Storage == nil {
		fail(w, http.StatusInternalServerError, errors.New("S3 service is not configured"))
		return
	}
	key := request.Prefix + request.Name
	uploadID, err := s.Storage.InitMultipart(r.Context(), c, key, request.ContentType)
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	s.uploadMu.Lock()
	if s.uploads == nil {
		s.uploads = make(map[string]*uploadState)
	}
	tempDir, tempErr := os.MkdirTemp("", "mariner-upload-")
	if tempErr != nil {
		s.uploadMu.Unlock()
		_ = s.Storage.AbortMultipart(r.Context(), c, uploadID, key)
		fail(w, http.StatusInternalServerError, tempErr)
		return
	}
	s.uploads[uploadID] = &uploadState{UserID: session.User.ID, Connection: c, Bucket: c.Bucket, Key: key, UploadID: uploadID, Parts: make(map[int32]model.UploadPart), PartPaths: make(map[int32]string), TempDir: tempDir}
	s.uploadMu.Unlock()
	slog.Info("multipart upload initialized", "upload_id", uploadID, "part_size", s.uploadPartSize(), "concurrency", s.uploadPartConcurrency(), "size", request.Size)
	write(w, map[string]any{"uploadId": uploadID, "key": key, "partSize": s.uploadPartSize(), "concurrency": s.uploadPartConcurrency()})
}

func (s *Server) uploadPart(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	uploadID := r.URL.Query().Get("uploadId")
	partNumber, parseErr := strconv.ParseInt(r.URL.Query().Get("partNumber"), 10, 32)
	partFields := func() []any {
		return []any{"upload_id", uploadID, "part_number", partNumber}
	}
	stageStarted := time.Now()
	session, _, err := s.session(r)
	if err != nil {
		fail(w, http.StatusUnauthorized, err)
		return
	}
	slog.Info("multipart part stage", append(partFields(), "stage", "session", "duration_ms", time.Since(stageStarted).Milliseconds())...)
	if uploadID == "" || parseErr != nil || partNumber < 1 {
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
	partSize := s.uploadPartSize()
	r.Body = http.MaxBytesReader(w, r.Body, partSize+1)
	stageStarted = time.Now()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if int64(len(body)) > partSize {
		fail(w, http.StatusRequestEntityTooLarge, errors.New("upload part is too large"))
		return
	}
	slog.Info("multipart part stage", append(partFields(), "stage", "read_request_body", "duration_ms", time.Since(stageStarted).Milliseconds(), "bytes", len(body), "content_length", r.ContentLength)...)
	if s.Storage == nil {
		fail(w, http.StatusInternalServerError, errors.New("S3 service is not configured"))
		return
	}
	stageStarted = time.Now()
	etag, err := s.Storage.UploadPart(r.Context(), state.Connection, uploadID, state.Key, int32(partNumber), body)
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	slog.Info("multipart part stage", append(partFields(), "stage", "s3_upload_part", "duration_ms", time.Since(stageStarted).Milliseconds(), "bytes", len(body))...)
	partPath := filepath.Join(state.TempDir, fmt.Sprintf("%08d.part", partNumber))
	stageStarted = time.Now()
	if err := os.WriteFile(partPath, body, 0600); err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	slog.Info("multipart part stage", append(partFields(), "stage", "write_temp_part", "duration_ms", time.Since(stageStarted).Milliseconds(), "bytes", len(body))...)
	stageStarted = time.Now()
	s.uploadMu.Lock()
	if _, exists := state.Parts[int32(partNumber)]; exists {
		s.uploadMu.Unlock()
		fail(w, http.StatusConflict, errors.New("multipart part was already uploaded"))
		return
	}
	state.Parts[int32(partNumber)] = model.UploadPart{ETag: etag, PartNumber: int32(partNumber)}
	state.PartPaths[int32(partNumber)] = partPath
	s.uploadMu.Unlock()
	slog.Info("multipart part completed", append(partFields(), "duration_ms", time.Since(started).Milliseconds(), "bytes", len(body))...)
	write(w, map[string]any{"partNumber": partNumber, "etag": etag})
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
	if s.Storage == nil {
		fail(w, http.StatusInternalServerError, errors.New("S3 service is not configured"))
		return
	}
	parts, err := orderedUploadParts(state.Parts)
	if err != nil {
		os.RemoveAll(state.TempDir)
		fail(w, http.StatusBadRequest, err)
		return
	}
	hasher := sha256.New()
	for _, part := range parts {
		file, openErr := os.Open(state.PartPaths[part.PartNumber])
		if openErr != nil {
			os.RemoveAll(state.TempDir)
			fail(w, http.StatusInternalServerError, openErr)
			return
		}
		_, copyErr := io.Copy(hasher, file)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			os.RemoveAll(state.TempDir)
			if copyErr != nil {
				fail(w, http.StatusInternalServerError, copyErr)
			} else {
				fail(w, http.StatusInternalServerError, closeErr)
			}
			return
		}
	}
	err = s.Storage.CompleteMultipart(r.Context(), c, uploadID, state.Key, parts)
	os.RemoveAll(state.TempDir)
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	s.audit(session, "object.upload", "success", map[string]string{"connection_id": c.ID, "bucket": c.Bucket, "object_key": state.Key, "file_sha256": digest})
	write(w, map[string]bool{"ok": true})
}

func orderedUploadParts(uploaded map[int32]model.UploadPart) ([]model.UploadPart, error) {
	if len(uploaded) == 0 {
		return nil, errors.New("multipart parts are incomplete")
	}
	parts := make([]model.UploadPart, 0, len(uploaded))
	for partNumber := int32(1); partNumber <= int32(len(uploaded)); partNumber++ {
		part, exists := uploaded[partNumber]
		if !exists {
			return nil, errors.New("multipart parts are incomplete")
		}
		parts = append(parts, part)
	}
	return parts, nil
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
	if s.Storage != nil {
		_ = s.Storage.AbortMultipart(r.Context(), state.Connection, uploadID, state.Key)
	}
	os.RemoveAll(state.TempDir)
	write(w, map[string]bool{"ok": true})
}

func (s *Server) uploadPartSize() int64 {
	if s.UploadPartSize > 0 {
		return s.UploadPartSize
	}
	return s3service.MultipartPartSize
}

func (s *Server) uploadPartConcurrency() int {
	if s.UploadPartConcurrency > 0 {
		return s.UploadPartConcurrency
	}
	return 1
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
	if s.Storage == nil {
		fail(w, http.StatusInternalServerError, errors.New("S3 service is not configured"))
		return
	}
	settings, err := s.applicationSettingsRepository().Get()
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
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
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", safeDownloadName(name+ext)))
	if err = s.Storage.DownloadArchive(r.Context(), c, prefix, format, settings.ArchiveMaxBytes, w); err != nil {
		return
	}
	s.audit(session, "object.download", "success", map[string]string{"connection_id": c.ID, "bucket": c.Bucket, "object_key": prefix})
}
func publicConnections(data vault.Data) []vault.Connection {
	result := make([]vault.Connection, 0, len(data.Connections))
	for _, c := range data.Connections {
		result = append(result, publicConnection(c))
	}
	return result
}
func publicConnection(connection vault.Connection) vault.Connection {
	response := mapper.ConnectionToResponse(connection)
	return vault.Connection{
		ID: response.ID, Name: response.Name, Bucket: response.Bucket,
		Region: response.Region, Endpoint: response.Endpoint, Prefix: response.Prefix,
	}
}
func safeDownloadName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	if name == "" {
		return "download"
	}
	return name
}
func randomID() string { return fmt.Sprintf("%d", time.Now().UnixNano()) }
func write(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, status int, err error) {
	if status >= http.StatusInternalServerError {
		slog.Error("request failed", "status", status, "error", err)
		err = errors.New("request could not be completed")
	}
	http.Error(w, err.Error(), status)
}
