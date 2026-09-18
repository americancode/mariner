package audit

import (
	"sync"
	"time"

	"github.com/google/uuid"
	"mariner/internal/vault"
)

// Logger writes the canonical JSON event to the database. Delivery to stdout
// is handled independently by the database-polling sidecar.
type Logger struct {
	store *vault.Store
	mu    sync.Mutex
}

func New(store *vault.Store) *Logger {
	return &Logger{store: store}
}

func (l *Logger) Write(event vault.AuditEvent) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.OccurredAt == "" {
		event.OccurredAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	_, err := l.store.AppendAudit(event)
	return err
}

func (l *Logger) Close() error { return nil }
