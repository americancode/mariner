-- +goose Up
-- Normalize timestamps historically written by the application and by
-- database CURRENT_TIMESTAMP. The application has always generated UTC
-- timestamps; this converts the legacy space-separated database format to
-- the canonical RFC3339 UTC representation without changing the instant.
-- The expression is valid for both SQLite and PostgreSQL. Historical database
-- timestamps are expected to have been generated in UTC.
UPDATE vaults
SET updated_at = CASE
    WHEN substr(updated_at, -1) = 'Z' THEN replace(updated_at, ' ', 'T')
    ELSE replace(replace(replace(updated_at, ' ', 'T'), '+00:00', ''), '+00', '') || 'Z'
END;

UPDATE audit_events
SET occurred_at = CASE
    WHEN substr(occurred_at, -1) = 'Z' THEN replace(occurred_at, ' ', 'T')
    ELSE replace(replace(replace(occurred_at, ' ', 'T'), '+00:00', ''), '+00', '') || 'Z'
END;

UPDATE organizations
SET updated_at = CASE
    WHEN substr(updated_at, -1) = 'Z' THEN replace(updated_at, ' ', 'T')
    ELSE replace(replace(replace(updated_at, ' ', 'T'), '+00:00', ''), '+00', '') || 'Z'
END;

UPDATE user_preferences
SET updated_at = CASE
    WHEN substr(updated_at, -1) = 'Z' THEN replace(updated_at, ' ', 'T')
    ELSE replace(replace(replace(updated_at, ' ', 'T'), '+00:00', ''), '+00', '') || 'Z'
END;

UPDATE application_settings
SET updated_at = CASE
    WHEN substr(updated_at, -1) = 'Z' THEN replace(updated_at, ' ', 'T')
    ELSE replace(replace(replace(updated_at, ' ', 'T'), '+00:00', ''), '+00', '') || 'Z'
END;

-- +goose Down
-- Timestamp normalization is intentionally irreversible. Existing canonical
-- UTC values must remain canonical on rollback.
SELECT 1;
