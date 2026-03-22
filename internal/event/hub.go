package event

import (
	"sync"

	"github.com/maniginam/waggle/internal/model"
)

type Subscriber struct {
	Ch       chan *model.Event
	AgentID  string
	TaskID   string
}

type Hub struct {
	mu          sync.RWMutex
	subscribers map[*Subscriber]struct{}
}

func NewHub() *Hub {
	return &Hub{
		subscribers: make(map[*Subscriber]struct{}),
	}
}

func (h *Hub) Subscribe(agentFilter, taskFilter string) *Subscriber {
	sub := &Subscriber{
		Ch:      make(chan *model.Event, 64),
		AgentID: agentFilter,
		TaskID:  taskFilter,
	}
	h.mu.Lock()
	h.subscribers[sub] = struct{}{}
	h.mu.Unlock()
	return sub
}

func (h *Hub) Unsubscribe(sub *Subscriber) {
	h.mu.Lock()
	delete(h.subscribers, sub)
	h.mu.Unlock()
	close(sub.Ch)
}

func (h *Hub) Publish(e *model.Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for sub := range h.subscribers {
		if sub.AgentID != "" && sub.AgentID != e.AgentID {
			continue
		}
		if sub.TaskID != "" && sub.TaskID != e.TaskID {
			continue
		}
		select {
		case sub.Ch <- e:
		default:
			// Drop if subscriber is slow
		}
	}
}

func (h *Hub) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subscribers)
}
