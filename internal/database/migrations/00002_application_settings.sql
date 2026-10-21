-- +goose Up
CREATE TABLE IF NOT EXISTS application_settings (
    name TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS application_settings;
