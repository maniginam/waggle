package event

import (
	"testing"
	"time"

	"github.com/maniginam/waggle/internal/model"
)

func TestPublishSubscribe(t *testing.T) {
	hub := NewHub()
	sub := hub.Subscribe("", "")
	defer hub.Unsubscribe(sub)

	evt := &model.Event{Type: model.EventTaskCreated, TaskID: "wg-123"}
	hub.Publish(evt)

	select {
	case got := <-sub.Ch:
		if got.TaskID != "wg-123" {
			t.Errorf("expected wg-123, got %s", got.TaskID)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestSubscribeWithAgentFilter(t *testing.T) {
	hub := NewHub()
	sub := hub.Subscribe("agent-1", "")
	defer hub.Unsubscribe(sub)

	// Should not receive - different agent
	hub.Publish(&model.Event{Type: model.EventTaskClaimed, AgentID: "agent-2"})

	// Should receive - matching agent
	hub.Publish(&model.Event{Type: model.EventTaskClaimed, AgentID: "agent-1"})

	select {
	case got := <-sub.Ch:
		if got.AgentID != "agent-1" {
			t.Errorf("expected agent-1, got %s", got.AgentID)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}

	// Verify no extra events
	select {
	case <-sub.Ch:
		t.Error("received unexpected event")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestSubscribeWithTaskFilter(t *testing.T) {
	hub := NewHub()
	sub := hub.Subscribe("", "wg-abc")
	defer hub.Unsubscribe(sub)

	hub.Publish(&model.Event{Type: model.EventTaskUpdated, TaskID: "wg-xyz"})
	hub.Publish(&model.Event{Type: model.EventTaskUpdated, TaskID: "wg-abc"})

	select {
	case got := <-sub.Ch:
		if got.TaskID != "wg-abc" {
			t.Errorf("expected wg-abc, got %s", got.TaskID)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestUnsubscribeClosesChannel(t *testing.T) {
	hub := NewHub()
	sub := hub.Subscribe("", "")
	hub.Unsubscribe(sub)

	_, ok := <-sub.Ch
	if ok {
		t.Error("expected channel to be closed")
	}
}

func TestSubscriberCount(t *testing.T) {
	hub := NewHub()
	if hub.SubscriberCount() != 0 {
		t.Errorf("expected 0, got %d", hub.SubscriberCount())
	}
	sub1 := hub.Subscribe("", "")
	sub2 := hub.Subscribe("", "")
	if hub.SubscriberCount() != 2 {
		t.Errorf("expected 2, got %d", hub.SubscriberCount())
	}
	hub.Unsubscribe(sub1)
	hub.Unsubscribe(sub2)
	if hub.SubscriberCount() != 0 {
		t.Errorf("expected 0, got %d", hub.SubscriberCount())
	}
}
