package overseer

import (
	"context"

	"github.com/maniginam/waggle/internal/model"
)

// Item is a normalized event plus a stable key used for change-dedup.
// Event.ID is left empty; the store assigns it on RecordEvent.
type Item struct {
	Key   string
	Event *model.Event
}

// Snapshot is one read of a source's current state.
type Snapshot struct {
	Items []Item
}

// Source is a read-only producer of normalized events from an engine.
// Poll must be non-blocking-ish and must never mutate the engine.
type Source interface {
	Name() string
	Poll(ctx context.Context) (Snapshot, error)
}
