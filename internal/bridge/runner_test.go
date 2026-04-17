package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestRunnerRegistersOnStart(t *testing.T) {
	var registered bool
	var regBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/agents/register" && r.Method == http.MethodPost:
			registered = true
			json.NewDecoder(r.Body).Decode(&regBody)
			json.NewEncoder(w).Encode(map[string]any{"id": "ag-1", "name": regBody["name"]})
		case r.URL.Path == "/api/messages" && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode([]any{})
		default:
			json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	r := NewRunner(&fakeBridge{provider: "test", caps: []Capability{CapChat}}, RunnerConfig{
		AgentName: "test-agent",
		BaseURL:   srv.URL,
		Mode:      ModeMessageOnly,
		ProjectID: "wg-123",
	})
	r.Run(ctx)

	if !registered {
		t.Error("expected runner to register agent on start")
	}
	if regBody["name"] != "test-agent" {
		t.Errorf("expected name 'test-agent', got %q", regBody["name"])
	}
}

func TestRunnerSendsHeartbeats(t *testing.T) {
	var mu sync.Mutex
	heartbeatCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/agents/register":
			json.NewEncoder(w).Encode(map[string]any{"id": "ag-1", "name": "hb-agent"})
		case r.URL.Path == "/api/agents/hb-agent/heartbeat":
			mu.Lock()
			heartbeatCount++
			mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		case r.URL.Path == "/api/messages":
			json.NewEncoder(w).Encode([]any{})
		default:
			json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()

	r := NewRunner(&fakeBridge{provider: "test", caps: []Capability{CapChat}}, RunnerConfig{
		AgentName:         "hb-agent",
		BaseURL:           srv.URL,
		Mode:              ModeMessageOnly,
		HeartbeatInterval: 100 * time.Millisecond,
	})
	r.Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	if heartbeatCount < 2 {
		t.Errorf("expected at least 2 heartbeats, got %d", heartbeatCount)
	}
}

func TestRunnerRespondsToMessages(t *testing.T) {
	var mu sync.Mutex
	var sentReply string
	messageServed := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/agents/register":
			json.NewEncoder(w).Encode(map[string]any{"id": "ag-1", "name": "msg-agent"})
		case r.URL.Path == "/api/messages" && r.Method == http.MethodGet:
			mu.Lock()
			served := messageServed
			mu.Unlock()
			if !served {
				mu.Lock()
				messageServed = true
				mu.Unlock()
				json.NewEncoder(w).Encode([]map[string]any{
					{"id": "m-1", "from": "user", "to": "msg-agent", "body": "hello", "read": false},
				})
			} else {
				json.NewEncoder(w).Encode([]any{})
			}
		case r.URL.Path == "/api/messages" && r.Method == http.MethodPost:
			var msg map[string]string
			json.NewDecoder(r.Body).Decode(&msg)
			mu.Lock()
			sentReply = msg["body"]
			mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		case r.URL.Path == "/api/messages" && r.Method == http.MethodPatch:
			json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		default:
			json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	r := NewRunner(&fakeBridge{response: "hi back", provider: "test", caps: []Capability{CapChat}}, RunnerConfig{
		AgentName:    "msg-agent",
		BaseURL:      srv.URL,
		Mode:         ModeMessageOnly,
		PollInterval: 50 * time.Millisecond,
	})
	r.Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	if sentReply != "hi back" {
		t.Errorf("expected reply 'hi back', got %q", sentReply)
	}
}

func TestRunnerDisconnectsOnShutdown(t *testing.T) {
	var disconnected bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/agents/register":
			json.NewEncoder(w).Encode(map[string]any{"id": "ag-1", "name": "dc-agent"})
		case r.URL.Path == "/api/agents/dc-agent/status" && r.Method == http.MethodPost:
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["status"] == "disconnected" {
				disconnected = true
			}
			json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		case r.URL.Path == "/api/messages":
			json.NewEncoder(w).Encode([]any{})
		default:
			json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	r := NewRunner(&fakeBridge{provider: "test", caps: []Capability{CapChat}}, RunnerConfig{
		AgentName: "dc-agent",
		BaseURL:   srv.URL,
		Mode:      ModeMessageOnly,
	})
	r.Run(ctx)

	if !disconnected {
		t.Error("expected runner to disconnect on shutdown")
	}
}

func TestRunnerFullParticipantClaimsTask(t *testing.T) {
	var mu sync.Mutex
	taskClaimed := false
	taskCompleted := false
	var commentBody string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch {
		case r.URL.Path == "/api/agents/register":
			json.NewEncoder(w).Encode(map[string]any{"id": "ag-1", "name": "worker"})

		case r.URL.Path == "/api/messages" && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode([]any{})

		case r.URL.Path == "/api/tasks" && r.Method == http.MethodGet:
			if !taskClaimed {
				json.NewEncoder(w).Encode([]map[string]any{
					{"id": "t-1", "title": "Write docs", "description": "Write API docs", "status": "ready",
						"criteria": []string{"clear", "concise"}},
				})
			} else {
				json.NewEncoder(w).Encode([]any{})
			}

		case r.URL.Path == "/api/tasks/t-1/claim":
			taskClaimed = true
			json.NewEncoder(w).Encode(map[string]any{"status": "ok"})

		case r.URL.Path == "/api/tasks/t-1/comments" && r.Method == http.MethodPost:
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			commentBody = body["body"]
			json.NewEncoder(w).Encode(map[string]any{"status": "ok"})

		case r.URL.Path == "/api/tasks/t-1/complete":
			taskCompleted = true
			json.NewEncoder(w).Encode(map[string]any{"status": "ok"})

		default:
			json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	r := NewRunner(
		&fakeBridge{response: "Here are the docs", provider: "test", caps: []Capability{CapChat, CapCode}},
		RunnerConfig{
			AgentName:    "worker",
			BaseURL:      srv.URL,
			Mode:         ModeFullParticipant,
			PollInterval: 50 * time.Millisecond,
			TaskInterval: 100 * time.Millisecond,
		},
	)
	r.Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	if !taskClaimed {
		t.Error("expected task to be claimed")
	}
	if commentBody != "Here are the docs" {
		t.Errorf("expected comment 'Here are the docs', got %q", commentBody)
	}
	if !taskCompleted {
		t.Error("expected task to be completed")
	}
}
