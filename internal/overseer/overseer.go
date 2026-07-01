package overseer

import (
	"context"
	"log"
	"time"

	"github.com/maniginam/waggle/internal/model"
)

type emitter interface {
	RecordEvent(*model.Event) error
}

type publisher interface {
	Publish(*model.Event)
}

type sourceConfig struct {
	src      Source
	interval time.Duration
	dedup    *deduper
}

type Overseer struct {
	store   emitter
	hub     publisher
	sources []sourceConfig
}

func New(store emitter, hub publisher) *Overseer {
	return &Overseer{store: store, hub: hub}
}

func (o *Overseer) Register(src Source, interval time.Duration) {
	o.sources = append(o.sources, sourceConfig{src: src, interval: interval, dedup: newDeduper(1000)})
}

// Run polls every registered source on its interval until ctx is cancelled.
func (o *Overseer) Run(ctx context.Context) {
	for i := range o.sources {
		go o.runSource(ctx, &o.sources[i])
	}
	<-ctx.Done()
}

func (o *Overseer) runSource(ctx context.Context, sc *sourceConfig) {
	ticker := time.NewTicker(sc.interval)
	defer ticker.Stop()
	o.pollOnce(ctx, sc) // immediate first poll
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			o.pollOnce(ctx, sc)
		}
	}
}

func (o *Overseer) pollOnce(ctx context.Context, sc *sourceConfig) {
	defer func() {
		if r := recover(); r != nil { // one bad source never kills the loop
			log.Printf("overseer: source %s panicked: %v", sc.src.Name(), r)
		}
	}()
	snap, err := sc.src.Poll(ctx)
	if err != nil {
		log.Printf("overseer: source %s poll: %v", sc.src.Name(), err)
		return
	}
	for _, it := range sc.dedup.filter(snap.Items) {
		_ = o.store.RecordEvent(it.Event)
		o.hub.Publish(it.Event)
	}
}
