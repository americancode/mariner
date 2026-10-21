package vault

import (
	"database/sql"
	"encoding/json"
	"errors"
	"sync"

	"github.com/jmoiron/sqlx"
)

// AuditEventTableRepository owns persistence and filtering for audit_events.
type AuditEventTableRepository struct {
	db *sqlx.DB
	mu *sync.Mutex
}

func newAuditEventTableRepository(db *sqlx.DB, mu *sync.Mutex) *AuditEventTableRepository {
	return &AuditEventTableRepository{db: db, mu: mu}
}

func (r *AuditEventTableRepository) Cursor() (string, string, error) {
	var occurredAt, eventID string
	err := r.db.QueryRowx("SELECT occurred_at, event_id FROM audit_events ORDER BY occurred_at DESC, event_id DESC LIMIT 1").Scan(&occurredAt, &eventID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return occurredAt, eventID, err
}

func (r *AuditEventTableRepository) ListAfter(occurredAt, eventID string, limit int) ([]AuditRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `SELECT event_id, occurred_at, event_json FROM audit_events
		WHERE occurred_at > ? OR (occurred_at = ? AND event_id > ?)
		ORDER BY occurred_at ASC, event_id ASC LIMIT ?`
	rows, err := r.db.Queryx(r.db.Rebind(query), occurredAt, occurredAt, eventID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]AuditRecord, 0, limit)
	for rows.Next() {
		var id, at, raw string
		if err := rows.Scan(&id, &at, &raw); err != nil {
			return nil, err
		}
		events = append(events, AuditRecord{ID: id, OccurredAt: at, JSON: raw})
	}
	return events, rows.Err()
}

func (r *AuditEventTableRepository) Append(event AuditEvent) (string, error) {
	raw, err := json.Marshal(event)
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err = r.db.Exec(r.db.Rebind(`INSERT INTO audit_events(event_id, occurred_at, event_json) VALUES(?,?,?)`), event.ID, event.OccurredAt, string(raw))
	return string(raw), err
}

func (r *AuditEventTableRepository) List(filter AuditFilter) (AuditPage, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	jsonField := func(name string) string {
		if r.db.DriverName() == "pgx" {
			return "(event_json::jsonb ->> '" + name + "')"
		}
		return "json_extract(event_json, '$." + name + "')"
	}
	query := "SELECT event_json FROM audit_events WHERE 1=1"
	args := make([]any, 0, 6)
	if filter.User != "" {
		query += " AND (" + jsonField("user_id") + " = ? OR " + jsonField("user_name") + " = ?)"
		args = append(args, filter.User, filter.User)
	}
	for _, condition := range []struct {
		value, expression string
	}{
		{filter.Bucket, jsonField("bucket")},
		{filter.Action, jsonField("action")},
	} {
		if condition.value == "" {
			continue
		}
		query += " AND " + condition.expression + " = ?"
		args = append(args, condition.value)
	}
	if filter.Start != "" {
		query += " AND occurred_at >= ?"
		args = append(args, filter.Start)
	}
	if filter.End != "" {
		query += " AND occurred_at <= ?"
		args = append(args, filter.End)
	}
	query += " ORDER BY occurred_at DESC, event_id DESC LIMIT ? OFFSET ?"
	args = append(args, limit+1, filter.Offset)
	rows, err := r.db.Queryx(r.db.Rebind(query), args...)
	if err != nil {
		return AuditPage{}, err
	}
	defer rows.Close()
	page := AuditPage{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return AuditPage{}, err
		}
		if len(page.Events) == limit {
			page.HasMore = true
			break
		}
		page.Events = append(page.Events, json.RawMessage(raw))
	}
	return page, rows.Err()
}

func (r *AuditEventTableRepository) ListActions() ([]string, error) {
	field := "json_extract(event_json, '$.action')"
	if r.db.DriverName() == "pgx" {
		field = "(event_json::jsonb ->> 'action')"
	}
	var actions []string
	if err := r.db.Select(&actions, "SELECT DISTINCT "+field+" FROM audit_events WHERE "+field+" IS NOT NULL AND "+field+" <> '' ORDER BY 1"); err != nil {
		return nil, err
	}
	return actions, nil
}
