package overseer

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/maniginam/waggle/internal/model"
)

type fakeStore struct {
	mu   sync.Mutex
	recs []*model.Event
}

func (f *fakeStore) RecordEvent(e *model.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recs = append(f.recs, e)
	return nil
}

type fakeHub struct {
	mu   sync.Mutex
	pubs []*model.Event
}

func (f *fakeHub) Publish(e *model.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pubs = append(f.pubs, e)
}

type panicSource struct{}

func (panicSource) Name() string                           { return "boom" }
func (panicSource) Poll(context.Context) (Snapshot, error) { panic("kaboom") }

func TestPollOnceEmitsDedupedEvents(t *testing.T) {
	fs, fh := &fakeStore{}, &fakeHub{}
	o := New(fs, fh)
	o.Register(&fakeSource{name: "fake", items: []Item{
		{Key: "k1", Event: &model.Event{Type: "task.queued", TaskID: "t1"}},
	}}, time.Hour)

	o.pollOnce(context.Background(), &o.sources[0])
	o.pollOnce(context.Background(), &o.sources[0]) // same state -> no new emit

	if len(fs.recs) != 1 || len(fh.pubs) != 1 {
		t.Fatalf("want 1 record + 1 publish, got recs=%d pubs=%d", len(fs.recs), len(fh.pubs))
	}
}

func TestPollOncePanicIsContained(t *testing.T) {
	o := New(&fakeStore{}, &fakeHub{})
	o.Register(panicSource{}, time.Hour)
	// must not panic out of pollOnce
	o.pollOnce(context.Background(), &o.sources[0])
}
