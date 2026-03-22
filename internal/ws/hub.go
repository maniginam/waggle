package ws

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/maniginam/waggle/internal/event"
	"github.com/maniginam/waggle/internal/model"
	"github.com/maniginam/waggle/internal/store"
	"golang.org/x/net/websocket"
)

const (
	heartbeatInterval = 30 * time.Second
	heartbeatTimeout  = 90 * time.Second
)

type Client struct {
	conn      *websocket.Conn
	agentName string
	send      chan []byte
	hub       *Hub
}

type Hub struct {
	mu       sync.RWMutex
	clients  map[*Client]struct{}
	store    *store.Store
	eventHub *event.Hub
}

func NewHub(s *store.Store, eh *event.Hub) *Hub {
	return &Hub{
		clients:  make(map[*Client]struct{}),
		store:    s,
		eventHub: eh,
	}
}

func (h *Hub) Handler() http.Handler {
	return websocket.Handler(h.handleConn)
}

func (h *Hub) handleConn(conn *websocket.Conn) {
	client := &Client{
		conn: conn,
		send: make(chan []byte, 64),
		hub:  h,
	}

	h.mu.Lock()
	h.clients[client] = struct{}{}
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.clients, client)
		h.mu.Unlock()
		if client.agentName != "" {
			h.store.DisconnectAgent(client.agentName)
			h.eventHub.Publish(&model.Event{
				Type:    model.EventAgentLeft,
				AgentID: client.agentName,
			})
		}
		conn.Close()
	}()

	// Subscribe to events and forward to client
	sub := h.eventHub.Subscribe("", "")
	defer h.eventHub.Unsubscribe(sub)

	go func() {
		for evt := range sub.Ch {
			data, _ := json.Marshal(evt)
			select {
			case client.send <- data:
			default:
			}
		}
	}()

	go client.writePump()
	client.readPump()
}

func (c *Client) readPump() {
	for {
		var raw string
		if err := websocket.Message.Receive(c.conn, &raw); err != nil {
			return
		}

		var msg struct {
			Action string          `json:"action"`
			Data   json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(raw), &msg); err != nil {
			continue
		}

		c.handleAction(msg.Action, msg.Data)
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case data, ok := <-c.send:
			if !ok {
				return
			}
			if err := websocket.Message.Send(c.conn, string(data)); err != nil {
				return
			}
		case <-ticker.C:
			ping := `{"type":"ping"}`
			if err := websocket.Message.Send(c.conn, ping); err != nil {
				return
			}
		}
	}
}

func (c *Client) handleAction(action string, data json.RawMessage) {
	switch action {
	case "register":
		var req struct {
			Name string `json:"name"`
			Type string `json:"type"`
		}
		json.Unmarshal(data, &req)
		if req.Name == "" {
			return
		}
		c.agentName = req.Name
		agent, err := c.hub.store.RegisterAgent(req.Name, req.Type)
		if err != nil {
			log.Printf("register agent error: %v", err)
			return
		}
		c.hub.eventHub.Publish(&model.Event{
			Type:    model.EventAgentJoined,
			AgentID: agent.Name,
			Payload: agent,
		})
		resp, _ := json.Marshal(map[string]any{"type": "registered", "agent": agent})
		c.send <- resp

	case "heartbeat":
		if c.agentName != "" {
			c.hub.store.TouchAgent(c.agentName)
		}

	case "claim":
		var req struct {
			TaskID string `json:"task_id"`
		}
		json.Unmarshal(data, &req)
		if c.agentName == "" || req.TaskID == "" {
			return
		}
		if err := c.hub.store.ClaimTask(req.TaskID, c.agentName); err != nil {
			errResp, _ := json.Marshal(map[string]any{"type": "error", "error": err.Error()})
			c.send <- errResp
			return
		}
		c.hub.store.RecordEvent(&model.Event{Type: model.EventTaskClaimed, AgentID: c.agentName, TaskID: req.TaskID})
		c.hub.eventHub.Publish(&model.Event{Type: model.EventTaskClaimed, AgentID: c.agentName, TaskID: req.TaskID})

	case "unclaim":
		var req struct {
			TaskID string `json:"task_id"`
		}
		json.Unmarshal(data, &req)
		if c.agentName == "" || req.TaskID == "" {
			return
		}
		if err := c.hub.store.UnclaimTask(req.TaskID, c.agentName); err != nil {
			errResp, _ := json.Marshal(map[string]any{"type": "error", "error": err.Error()})
			c.send <- errResp
			return
		}
		c.hub.store.RecordEvent(&model.Event{Type: model.EventTaskUnclaimed, AgentID: c.agentName, TaskID: req.TaskID})
		c.hub.eventHub.Publish(&model.Event{Type: model.EventTaskUnclaimed, AgentID: c.agentName, TaskID: req.TaskID})

	case "complete":
		var req struct {
			TaskID string `json:"task_id"`
		}
		json.Unmarshal(data, &req)
		if req.TaskID == "" {
			return
		}
		if err := c.hub.store.CompleteTask(req.TaskID); err != nil {
			errResp, _ := json.Marshal(map[string]any{"type": "error", "error": err.Error()})
			c.send <- errResp
			return
		}
		c.hub.store.RecordEvent(&model.Event{Type: model.EventTaskCompleted, AgentID: c.agentName, TaskID: req.TaskID})
		c.hub.eventHub.Publish(&model.Event{Type: model.EventTaskCompleted, AgentID: c.agentName, TaskID: req.TaskID})

	case "message":
		var req struct {
			To   string `json:"to"`
			Body string `json:"body"`
		}
		json.Unmarshal(data, &req)
		if c.agentName == "" || req.Body == "" {
			return
		}
		msg := &model.Message{From: c.agentName, To: req.To, Body: req.Body}
		c.hub.store.SendMessage(msg)
		c.hub.store.RecordEvent(&model.Event{Type: model.EventMessage, AgentID: c.agentName, Payload: msg})
		c.hub.eventHub.Publish(&model.Event{Type: model.EventMessage, AgentID: c.agentName, Payload: msg})
	}
}

func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}
