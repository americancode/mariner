-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS vaults (
    user_id TEXT PRIMARY KEY,
    salt TEXT NOT NULL,
    nonce TEXT NOT NULL,
    ciphertext TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_events (
    event_id TEXT PRIMARY KEY,
    occurred_at TEXT NOT NULL,
    event_json TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS organizations (
    id TEXT PRIMARY KEY,
    encrypted TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS user_preferences (
    user_id TEXT PRIMARY KEY,
    settings_json TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS user_preferences;
DROP TABLE IF EXISTS organizations;
DROP TABLE IF EXISTS audit_events;
DROP TABLE IF EXISTS vaults;
-- +goose StatementEnd
