package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"

	"periscope/internal/vault"
)

type Organization = vault.Organization
type OrganizationConnection = vault.OrganizationConnection

type Config struct {
	Addr, DataDir, DatabaseDriver, DatabaseURL, AuditAdminGroup, OrganizationEncryptionKey, OIDCIssuer, OIDCClientID, OIDCClientSecret, OIDCRedirectURL, CookieSecret, OIDCGroupsClaim, OIDCAudienceClaim, OIDCAudience, OIDCNameClaim string
	AuditEnabled                                                                                                                                                                                                                       bool
	OIDCDebugJWT                                                                                                                                                                                                                       bool
	Organizations                                                                                                                                                                                                                      map[string]Organization
}

func Load() Config {
	databaseDriver := value("DATABASE_DRIVER", "postgres")
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" && databaseDriver == "postgres" {
		databaseURL = postgresURL()
	}
	cfg := Config{Addr: value("ADDR", ":8080"), DataDir: value("DATA_DIR", "./data"), DatabaseDriver: databaseDriver, DatabaseURL: databaseURL, AuditAdminGroup: value("AUDIT_ADMIN_GROUP", "admins"), OrganizationEncryptionKey: os.Getenv("periscope_ORG_ENCRYPTION_KEY"), AuditEnabled: os.Getenv("AUDIT_ENABLED") != "false", OIDCIssuer: os.Getenv("OIDC_ISSUER"), OIDCClientID: os.Getenv("OIDC_CLIENT_ID"), OIDCClientSecret: os.Getenv("OIDC_CLIENT_SECRET"), OIDCRedirectURL: os.Getenv("OIDC_REDIRECT_URL"), CookieSecret: value("COOKIE_SECRET", "change-me-in-production"), OIDCGroupsClaim: value("OIDC_GROUPS_CLAIM", "groups"), OIDCAudienceClaim: value("OIDC_AUDIENCE_CLAIM", "aud"), OIDCAudience: value("OIDC_AUDIENCE", os.Getenv("OIDC_CLIENT_ID")), OIDCNameClaim: value("OIDC_NAME_CLAIM", "name"), OIDCDebugJWT: os.Getenv("OIDC_DEBUG_JWT") == "true"}
	if raw := os.Getenv("periscope_ORGANIZATIONS_JSON"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg.Organizations); err != nil {
			panic(fmt.Sprintf("invalid periscope_ORGANIZATIONS_JSON: %v", err))
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
