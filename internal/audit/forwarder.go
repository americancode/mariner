package audit

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"mariner/internal/repository"
)

// RunForwarder emits new database audit JSON events to stdout. The cursor is
// initialized to the latest row so a sidecar restart does not replay history.
func RunForwarder(ctx context.Context, store repository.AuditEventRepository, interval time.Duration, batchSize int) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	if batchSize <= 0 {
		batchSize = 100
	}
	at, id, err := store.Cursor()
	if err != nil {
		slog.Error("audit forwarder cursor initialization failed", "error", err)
		return err
	}
	slog.Info("audit forwarder started", "interval", interval, "batch_size", batchSize)
	for {
		events, err := store.ListAfter(at, id, batchSize)
		if err != nil {
			slog.Error("audit forwarder database poll failed", "error", err)
		} else {
			slog.Debug("audit forwarder poll completed", "events", len(events))
			for _, event := range events {
				// The sidecar contract requires the canonical event JSON unchanged.
				fmt.Fprintln(os.Stdout, event.JSON)
				slog.Debug("audit event forwarded", "event_id", event.ID, "occurred_at", event.OccurredAt)
				at, id = event.OccurredAt, event.ID
			}
		}
		select {
		case <-ctx.Done():
			slog.Info("audit forwarder stopped")
			return nil
		case <-time.After(interval):
		}
	}
}
