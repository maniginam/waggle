package telegram

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maniginam/waggle/internal/event"
	"github.com/maniginam/waggle/internal/model"
)

func TestNotifierSendsWhitelistedEvent(t *testing.T) {
	hub := event.NewHub()
	var sink []string
	var mu sync.Mutex
	tg := fakeTelegram(t, &sink, &mu)
	n := NewNotifier(hub, tg, []int64{111})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go n.Run(ctx)
	time.Sleep(20 * time.Millisecond) // let the subscription register

	hub.Publish(&model.Event{Type: model.EventTaskCreated, TaskID: "t1", Payload: map[string]any{"title": "hi"}})
	// a non-whitelisted event should NOT be sent
	hub.Publish(&model.Event{Type: model.EventTaskUpdated, TaskID: "t2"})

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		joined := strings.Join(sink, "\n")
		mu.Unlock()
		if strings.Contains(joined, "t1") || strings.Contains(joined, "hi") {
			if strings.Contains(joined, "t2") {
				t.Fatalf("non-whitelisted event was sent: %s", joined)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("expected a message for the whitelisted event")
}

func TestNotifierSuppressesSelfAction(t *testing.T) {
	hub := event.NewHub()
	var sink []string
	var mu sync.Mutex
	tg := fakeTelegram(t, &sink, &mu)
	n := NewNotifier(hub, tg, []int64{111})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go n.Run(ctx)
	time.Sleep(20 * time.Millisecond)

	n.Suppress("t9")
	hub.Publish(&model.Event{Type: model.EventTaskCompleted, TaskID: "t9"})

	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	joined := strings.Join(sink, "\n")
	mu.Unlock()
	if strings.Contains(joined, "t9") {
		t.Fatalf("suppressed event was still sent: %s", joined)
	}
}
