package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
	"mariner/internal/audit"
	"mariner/internal/auth"
	"mariner/internal/config"
	"mariner/internal/httpapi"
	"mariner/internal/vault"
	_ "modernc.org/sqlite"
)

func main() {
	// Container runtimes collect stdout and stderr separately. Keep all
	// application diagnostics on stdout so startup/configuration failures are
	// visible to the same log pipeline as normal application output.
	log.SetOutput(os.Stdout)
	cfg := config.Load()
	store, err := vault.OpenDatabase(cfg.DatabaseDriver, cfg.DataDir, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database initialization failed (driver=%s): %v", cfg.DatabaseDriver, err)
	}
	defer store.Close()
	store.SetOrganizationEncryptionKey(cfg.OrganizationEncryptionKey)
	if len(os.Args) > 1 && os.Args[1] == "audit-forwarder" {
		log.Fatal(audit.RunForwarder(context.Background(), store, cfg.AuditForwarderPollingInterval, cfg.AuditForwarderBatchSize))
	}
	var auditLogger *audit.Logger
	if cfg.AuditEnabled {
		auditLogger = audit.New(store)
		defer auditLogger.Close()
	}
	if cfg.OIDCIssuer == "" {
		log.Fatal("configuration error: OIDC_ISSUER is required")
	}
	authService, err := auth.New(cfg.OIDCIssuer, cfg.OIDCClientID, cfg.OIDCClientSecret, cfg.OIDCRedirectURL, cfg.CookieSecret, cfg.OIDCGroupsClaim, cfg.OIDCAudienceClaim, cfg.OIDCAudience, cfg.OIDCNameClaim, cfg.OIDCDebugJWT)
	if err != nil {
		discoveryURL := strings.TrimRight(cfg.OIDCIssuer, "/") + "/.well-known/openid-configuration"
		log.Fatalf("OIDC initialization failed (issuer=%s, discovery=%s): %v", cfg.OIDCIssuer, discoveryURL, err)
	}
	server := &httpapi.Server{Auth: authService, Vault: store, Audit: auditLogger, AuditAdminGroup: cfg.AuditAdminGroup, Organizations: cfg.Organizations}
	log.Printf("mariner listening on %s", cfg.Addr)
	log.Fatal(http.ListenAndServe(cfg.Addr, server.Router()))
}
