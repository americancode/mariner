package audit

import (
	"sync"
	"time"

	"github.com/google/uuid"
	"mariner/internal/model"
	"mariner/internal/repository"
)

// Logger writes the canonical JSON event to the database. Delivery to stdout
// is handled independently by the database-polling sidecar.
type Logger struct {
	store repository.AuditEventRepository
	mu    sync.Mutex
}

func New(store repository.AuditEventRepository) *Logger {
	return &Logger{store: store}
}

func (l *Logger) Write(event model.AuditEvent) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.OccurredAt == "" {
		event.OccurredAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	_, err := l.store.Append(event)
	return err
}

func (l *Logger) Close() error { return nil }
