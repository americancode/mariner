package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"mariner/internal/api"
	"mariner/internal/audit"
	"mariner/internal/auth"
	"mariner/internal/config"
	"mariner/internal/httpapi"
	"mariner/internal/logging"
	s3service "mariner/internal/service/s3"
	"mariner/internal/vault"
	_ "modernc.org/sqlite"
)

func main() {
	// Container runtimes collect stdout and stderr separately. Keep all
	// application diagnostics on stdout so startup/configuration failures are
	// visible to the same log pipeline as normal application output.
	logging.Configure(os.Getenv("LOG_LEVEL"))
	cfg := config.Load()
	if len(os.Args) > 1 && os.Args[1] == "audit-forwarder" {
		logging.ConfigureWriter(cfg.LogLevel, os.Stderr)
	} else {
		logging.Configure(cfg.LogLevel)
	}
	slog.Info("mariner starting", "addr", cfg.Addr, "database_driver", cfg.DatabaseDriver, "log_level", cfg.LogLevel)
	store, err := vault.OpenDatabase(cfg.DatabaseDriver, cfg.DataDir, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database initialization failed", "driver", cfg.DatabaseDriver, "error", err)
		os.Exit(1)
	}
	defer store.Close()
	store.SetOrganizationEncryptionKey(cfg.OrganizationEncryptionKey)
	if len(os.Args) > 1 && os.Args[1] == "audit-forwarder" {
		if err := audit.RunForwarder(context.Background(), store.AuditRepository(), cfg.AuditForwarderPollingInterval, cfg.AuditForwarderBatchSize); err != nil {
			slog.Error("audit forwarder stopped", "error", err)
			os.Exit(1)
		}
	}
	var auditLogger *audit.Logger
	if cfg.AuditEnabled {
		auditLogger = audit.New(store.AuditRepository())
		defer auditLogger.Close()
	}
	if cfg.OIDCIssuer == "" {
		slog.Error("configuration error", "error", "OIDC_ISSUER is required")
		os.Exit(1)
	}
	authService, err := auth.New(cfg.OIDCIssuer, cfg.OIDCClientID, cfg.OIDCClientSecret, cfg.OIDCRedirectURL, cfg.CookieSecret, cfg.OIDCGroupsClaim, cfg.OIDCAudienceClaim, cfg.OIDCAudience, cfg.OIDCNameClaim, cfg.OIDCScopes, cfg.OIDCDebugJWT, cfg.OIDCLogoutEnabled)
	if err != nil {
		discoveryURL := strings.TrimRight(cfg.OIDCIssuer, "/") + "/.well-known/openid-configuration"
		slog.Error("OIDC initialization failed", "issuer", cfg.OIDCIssuer, "discovery", discoveryURL, "error", err)
		os.Exit(1)
	}
	storage := s3service.NewWithUploadSettings(cfg.UploadPartSize, cfg.UploadPartConcurrency)
	server := &httpapi.Server{Auth: authService, Vault: store, Audit: auditLogger, AuditRepo: store.AuditRepository(), OrganizationRepo: store.OrganizationRepository(), PreferenceRepo: store.UserPreferenceRepository(), ApplicationSettingsRepo: store.ApplicationSettingsRepository(), Storage: storage, AuditAdminGroup: cfg.AuditAdminGroup, Organizations: cfg.Organizations, RateLimitEnabled: cfg.RateLimitEnabled, RateLimitWindow: cfg.RateLimitWindow, LoginRateLimit: cfg.LoginRateLimit, UnlockRateLimit: cfg.UnlockRateLimit, ConnectionTestRateLimit: cfg.ConnectionTestRateLimit, UploadPartSize: cfg.UploadPartSize, UploadPartConcurrency: cfg.UploadPartConcurrency}
	server.Connections = api.NewConnectionAPI(store.VaultRepository(), storage, uuid.NewString, server.IsOrganizationConnection)
	slog.Info("mariner listening", "addr", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, server.Router()); err != nil {
		slog.Error("HTTP server stopped", "error", err)
		os.Exit(1)
	}
}
