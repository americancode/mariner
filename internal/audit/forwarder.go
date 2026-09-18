package audit

import (
	"context"
	"log"
	"time"

	"mariner/internal/vault"
)

// RunForwarder emits new database audit JSON events to stdout. The cursor is
// initialized to the latest row so a sidecar restart does not replay history.
func RunForwarder(ctx context.Context, store *vault.Store, interval time.Duration, batchSize int) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	if batchSize <= 0 {
		batchSize = 100
	}
	at, id, err := store.AuditCursor()
	if err != nil {
		return err
	}
	for {
		events, err := store.ListAuditAfter(at, id, batchSize)
		if err != nil {
			log.Printf("audit forwarder database poll failed: %v", err)
		} else {
			for _, event := range events {
				log.Printf("%s", event.JSON)
				at, id = event.OccurredAt, event.ID
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(interval):
		}
	}
}
