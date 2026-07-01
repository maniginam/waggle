package overseer

import (
	"context"
	"testing"

	"github.com/maniginam/waggle/internal/model"
)

type fakeSource struct {
	name  string
	items []Item
}

func (f *fakeSource) Name() string { return f.name }
func (f *fakeSource) Poll(ctx context.Context) (Snapshot, error) {
	return Snapshot{Items: f.items}, nil
}

func TestSourcePollReturnsItems(t *testing.T) {
	var s Source = &fakeSource{
		name:  "fake",
		items: []Item{{Key: "k1", Event: &model.Event{Type: "task.queued", TaskID: "t1"}}},
	}
	snap, err := s.Poll(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(snap.Items) != 1 || snap.Items[0].Key != "k1" {
		t.Fatalf("got %+v", snap.Items)
	}
	if s.Name() != "fake" {
		t.Fatalf("name = %q", s.Name())
	}
}
