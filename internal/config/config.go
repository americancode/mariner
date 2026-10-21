package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	"mariner/internal/vault"
)

type Organization = vault.Organization
type OrganizationConnection = vault.OrganizationConnection

type Config struct {
	Addr, DataDir, DatabaseDriver, DatabaseURL, AuditAdminGroup, OrganizationEncryptionKey, OIDCIssuer, OIDCClientID, OIDCClientSecret, OIDCRedirectURL, CookieSecret, OIDCGroupsClaim, OIDCAudienceClaim, OIDCAudience, OIDCNameClaim string
	OIDCScopes                                                                                                                                                                                                                         []string
	AuditForwarderPollingInterval                                                                                                                                                                                                      time.Duration
	AuditForwarderBatchSize                                                                                                                                                                                                            int
	AuditEnabled, OIDCLogoutEnabled                                                                                                                                                                                                    bool
	OIDCDebugJWT                                                                                                                                                                                                                       bool
	RateLimitEnabled                                                                                                                                                                                                                   bool
	RateLimitWindow                                                                                                                                                                                                                    time.Duration
	LoginRateLimit, UnlockRateLimit, ConnectionTestRateLimit                                                                                                                                                                           int
	UploadPartSize                                                                                                                                                                                                                     int64
	UploadPartConcurrency                                                                                                                                                                                                              int
	LogLevel                                                                                                                                                                                                                           string
	Organizations                                                                                                                                                                                                                      map[string]Organization
}

func Load() Config {
	databaseDriver := value("DATABASE_DRIVER", "postgres")
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" && databaseDriver == "postgres" {
		databaseURL = postgresURL()
	}
	cfg := Config{Addr: value("ADDR", ":8080"), DataDir: value("DATA_DIR", "./data"), DatabaseDriver: databaseDriver, DatabaseURL: databaseURL, AuditAdminGroup: value("AUDIT_ADMIN_GROUP", "admins"), OrganizationEncryptionKey: os.Getenv("MARINER_ORG_ENCRYPTION_KEY"), AuditForwarderPollingInterval: durationValue("AUDIT_FORWARDER_POLLING_INTERVAL", 2*time.Second), AuditForwarderBatchSize: valueInt("AUDIT_FORWARDER_BATCH_SIZE", 100), AuditEnabled: os.Getenv("AUDIT_ENABLED") != "false", OIDCLogoutEnabled: os.Getenv("OIDC_LOGOUT_ENABLED") != "false", OIDCIssuer: os.Getenv("OIDC_ISSUER"), OIDCClientID: os.Getenv("OIDC_CLIENT_ID"), OIDCClientSecret: os.Getenv("OIDC_CLIENT_SECRET"), OIDCRedirectURL: os.Getenv("OIDC_REDIRECT_URL"), CookieSecret: value("COOKIE_SECRET", "change-me-in-production"), OIDCGroupsClaim: value("OIDC_GROUPS_CLAIM", "groups"), OIDCAudienceClaim: value("OIDC_AUDIENCE_CLAIM", "aud"), OIDCAudience: value("OIDC_AUDIENCE", os.Getenv("OIDC_CLIENT_ID")), OIDCNameClaim: value("OIDC_NAME_CLAIM", "name"), OIDCScopes: strings.Fields(value("OIDC_SCOPES", "openid profile email")), OIDCDebugJWT: os.Getenv("OIDC_DEBUG_JWT") == "true", RateLimitEnabled: os.Getenv("RATE_LIMIT_ENABLED") != "false", RateLimitWindow: durationValue("RATE_LIMIT_WINDOW", time.Minute), LoginRateLimit: valueInt("RATE_LIMIT_LOGIN", 10), UnlockRateLimit: valueInt("RATE_LIMIT_UNLOCK", 10), ConnectionTestRateLimit: valueInt("RATE_LIMIT_CONNECTION_TEST", 30), UploadPartSize: valueInt64("UPLOAD_PART_SIZE", 16*1024*1024), UploadPartConcurrency: valueInt("UPLOAD_PART_CONCURRENCY", 1), LogLevel: value("LOG_LEVEL", "info")}
	if raw := os.Getenv("MARINER_ORGANIZATIONS_JSON"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg.Organizations); err != nil {
			slog.Error("configuration error", "key", "MARINER_ORGANIZATIONS_JSON", "error", err)
			os.Exit(1)
		}
	}
	for orgName, organization := range cfg.Organizations {
		if organization.ID == "" {
			organization.ID = orgName
		}
		if organization.Name == "" {
			organization.Name = orgName
		}
		organization.Provisioned = true
		if len(organization.Groups) == 0 {
			organization.Groups = []string{orgName}
		}
		for name, configured := range organization.Connections {
			c := configured
			if c.ID == "" {
				c.ID = name
			}
			if c.Name == "" {
				c.Name = name
			}
			if c.AccessKeyEnv != "" {
				c.AccessKey = os.Getenv(c.AccessKeyEnv)
			}
			if c.SecretKeyEnv != "" {
				c.SecretKey = os.Getenv(c.SecretKeyEnv)
			}
			organization.Connections[name] = c
		}
		cfg.Organizations[orgName] = organization
	}
	return cfg
}

func postgresURL() string {
	host := os.Getenv("DATABASE_HOST")
	port := value("DATABASE_PORT", "5432")
	database := os.Getenv("DATABASE_NAME")
	username := os.Getenv("DATABASE_USERNAME")
	password := os.Getenv("DATABASE_PASSWORD")
	if host == "" || database == "" || username == "" || password == "" {
		return ""
	}
	query := url.Values{}
	if sslMode := os.Getenv("DATABASE_SSLMODE"); sslMode != "" {
		query.Set("sslmode", sslMode)
	}
	return (&url.URL{Scheme: "postgres", Host: host + ":" + port, Path: "/" + database, User: url.UserPassword(username, password), RawQuery: query.Encode()}).String()
}

func value(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func valueInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		var parsed int
		if _, err := fmt.Sscanf(v, "%d", &parsed); err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
}

func valueInt64(key string, fallback int64) int64 {
	if v := os.Getenv(key); v != "" {
		var parsed int64
		if _, err := fmt.Sscanf(v, "%d", &parsed); err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
}

func durationValue(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(v)
	if err == nil && parsed > 0 {
		return parsed
	}
	slog.Error("configuration error", "key", key, "value", v, "error", "must be a positive duration")
	os.Exit(1)
	return fallback
}
