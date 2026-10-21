package model

import "encoding/json"

type AuditEvent struct {
	ID           string `json:"event_id"`
	OccurredAt   string `json:"occurred_at"`
	Action       string `json:"action"`
	Result       string `json:"result"`
	UserID       string `json:"user_id,omitempty"`
	UserName     string `json:"user_name,omitempty"`
	Organization string `json:"organization,omitempty"`
	ConnectionID string `json:"connection_id,omitempty"`
	Bucket       string `json:"bucket,omitempty"`
	ObjectKey    string `json:"object_key,omitempty"`
	FileSHA256   string `json:"file_sha256,omitempty"`
	RequestID    string `json:"request_id,omitempty"`
	Error        string `json:"error,omitempty"`
}

type AuditFilter struct {
	User, Bucket, Action, Start, End string
	Limit, Offset                    int
}

type AuditPage struct {
	Events  []json.RawMessage
	HasMore bool
}

type AuditRecord struct {
	ID, OccurredAt, JSON string
}
