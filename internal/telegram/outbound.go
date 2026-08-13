package telegram

import (
	"context"
	"fmt"
	"sync"

	"github.com/maniginam/waggle/internal/event"
	"github.com/maniginam/waggle/internal/model"
)

type Notifier struct {
	hub      *event.Hub
	tg       *Client
	chats    []int64
	mu       sync.Mutex
	suppress map[string]int
}

func NewNotifier(hub *event.Hub, tg *Client, chats []int64) *Notifier {
	return &Notifier{hub: hub, tg: tg, chats: chats, suppress: map[string]int{}}
}

func (n *Notifier) Suppress(taskID string) {
	if taskID == "" {
		return
	}
	n.mu.Lock()
	n.suppress[taskID]++
	n.mu.Unlock()
}

// consumeSuppression returns true if this event should be skipped (and consumes one token).
func (n *Notifier) consumeSuppression(taskID string) bool {
	if taskID == "" {
		return false
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.suppress[taskID] > 0 {
		n.suppress[taskID]--
		if n.suppress[taskID] == 0 {
			delete(n.suppress, taskID)
		}
		return true
	}
	return false
}

func (n *Notifier) format(e *model.Event) (string, bool) {
	switch e.Type {
	case model.EventTaskCreated:
		return fmt.Sprintf("New task %s", e.TaskID), true
	case model.EventTaskCompleted:
		return fmt.Sprintf("Task completed: %s", e.TaskID), true
	case model.EventMessage:
		return fmt.Sprintf("Message from %s", e.AgentID), true
	default:
		return "", false
	}
}

func (n *Notifier) Run(ctx context.Context) {
	sub := n.hub.Subscribe("", "")
	defer n.hub.Unsubscribe(sub)
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-sub.Ch:
			if !ok {
				return
			}
			msg, send := n.format(e)
			if !send {
				continue
			}
			if n.consumeSuppression(e.TaskID) {
				continue
			}
			for _, chat := range n.chats {
				n.tg.SendMessage(ctx, chat, msg, nil)
			}
		}
	}
}
