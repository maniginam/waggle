package ws

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/maniginam/waggle/internal/event"
	"github.com/maniginam/waggle/internal/model"
	"github.com/maniginam/waggle/internal/store"
	"golang.org/x/net/websocket"
)

func setupWSHub(t *testing.T) (*Hub, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	eh := event.NewHub()
	hub := NewHub(s, eh)

	// Start HTTP server with WS handler
	mux := newTestMux(hub)
	server := newTestServer(t, mux)
	return hub, server
}

func TestWebSocketConnect(t *testing.T) {
	hub, serverURL := setupWSHub(t)

	wsURL := "ws" + serverURL[4:] + "/ws"
	origin := serverURL
	conn, err := websocket.Dial(wsURL, "", origin)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Give it a moment to register
	time.Sleep(50 * time.Millisecond)

	if hub.ClientCount() != 1 {
		t.Errorf("expected 1 client, got %d", hub.ClientCount())
	}
}

func TestWebSocketRegister(t *testing.T) {
	_, serverURL := setupWSHub(t)

	wsURL := "ws" + serverURL[4:] + "/ws"
	conn, err := websocket.Dial(wsURL, "", serverURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Send register action
	msg := `{"action":"register","data":{"name":"ws-test-agent","type":"claude-code"}}`
	websocket.Message.Send(conn, msg)

	// Read response
	var resp string
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	websocket.Message.Receive(conn, &resp)

	var parsed map[string]any
	json.Unmarshal([]byte(resp), &parsed)

	if parsed["type"] != "registered" {
		t.Errorf("expected 'registered' response, got %v", parsed["type"])
	}
}

func TestWebSocketReceivesEvents(t *testing.T) {
	hub, serverURL := setupWSHub(t)

	wsURL := "ws" + serverURL[4:] + "/ws"
	conn, err := websocket.Dial(wsURL, "", serverURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Wait for connection to be established
	time.Sleep(50 * time.Millisecond)

	// Publish event through event hub
	hub.eventHub.Publish(&model.Event{Type: model.EventTaskCreated, TaskID: "wg-test"})

	// Read the event from WS
	var resp string
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	err = websocket.Message.Receive(conn, &resp)
	if err != nil {
		t.Fatal(err)
	}

	var evt map[string]any
	json.Unmarshal([]byte(resp), &evt)
	if evt["task_id"] != "wg-test" {
		t.Errorf("expected task_id wg-test, got %v", evt["task_id"])
	}
}

func TestWebSocketDisconnectCleansUp(t *testing.T) {
	hub, serverURL := setupWSHub(t)

	wsURL := "ws" + serverURL[4:] + "/ws"
	conn, err := websocket.Dial(wsURL, "", serverURL)
	if err != nil {
		t.Fatal(err)
	}

	// Register
	websocket.Message.Send(conn, `{"action":"register","data":{"name":"disconnect-agent","type":"test"}}`)
	time.Sleep(50 * time.Millisecond)

	if hub.ClientCount() != 1 {
		t.Errorf("expected 1 client, got %d", hub.ClientCount())
	}

	// Disconnect
	conn.Close()
	time.Sleep(100 * time.Millisecond)

	if hub.ClientCount() != 0 {
		t.Errorf("expected 0 clients after disconnect, got %d", hub.ClientCount())
	}
}
