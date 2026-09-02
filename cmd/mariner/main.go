package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"periscope/internal/audit"
	"periscope/internal/auth"
	"periscope/internal/config"
	"periscope/internal/httpapi"
	"periscope/internal/vault"
	_ "modernc.org/sqlite"
)

func main() {
	cfg := config.Load()
	store, err := vault.OpenDatabase(cfg.DatabaseDriver, cfg.DataDir, cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	store.SetOrganizationEncryptionKey(cfg.OrganizationEncryptionKey)
	if len(os.Args) > 1 && os.Args[1] == "audit-forwarder" {
		log.Fatal(audit.RunForwarder(context.Background(), store, 2*time.Second))
	}
	var auditLogger *audit.Logger
	if cfg.AuditEnabled {
		auditLogger = audit.New(store)
		defer auditLogger.Close()
	}
	if cfg.OIDCIssuer == "" {
		log.Fatal("OIDC_ISSUER is required")
	}
	authService, err := auth.New(cfg.OIDCIssuer, cfg.OIDCClientID, cfg.OIDCClientSecret, cfg.OIDCRedirectURL, cfg.CookieSecret, cfg.OIDCGroupsClaim, cfg.OIDCAudienceClaim, cfg.OIDCAudience, cfg.OIDCNameClaim, cfg.OIDCDebugJWT)
	if err != nil {
		log.Fatal(err)
	}
	server := &httpapi.Server{Auth: authService, Vault: store, Audit: auditLogger, AuditAdminGroup: cfg.AuditAdminGroup, Organizations: cfg.Organizations}
	log.Printf("periscope listening on %s", cfg.Addr)
	log.Fatal(http.ListenAndServe(cfg.Addr, server.Router()))
}
