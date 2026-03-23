package ws

import (
	"encoding/json"
	"os"
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
	dir, err := os.MkdirTemp("", "waggle-ws-test-*")
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}

	eh := event.NewHub()
	hub := NewHub(s, eh)

	mux := newTestMux(hub)
	server := newTestServer(t, mux)

	// Cleanup in correct order: server closes first, then store, then dir
	// We register this as a single cleanup to control ordering
	t.Cleanup(func() {
		s.Close()
		time.Sleep(50 * time.Millisecond) // Let SQLite release WAL files
		os.RemoveAll(dir)
	})

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

func connectAndRegister(t *testing.T, serverURL, agentName string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + serverURL[4:] + "/ws"
	conn, err := websocket.Dial(wsURL, "", serverURL)
	if err != nil {
		t.Fatal(err)
	}
	msg := `{"action":"register","data":{"name":"` + agentName + `","type":"test"}}`
	websocket.Message.Send(conn, msg)
	// Drain the "registered" response and the "agent_joined" event broadcast
	for i := 0; i < 2; i++ {
		var resp string
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		websocket.Message.Receive(conn, &resp)
	}
	conn.SetReadDeadline(time.Time{})
	return conn
}

func readWSMessage(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	var resp string
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := websocket.Message.Receive(conn, &resp); err != nil {
		t.Fatalf("failed to read WS message: %v", err)
	}
	var parsed map[string]any
	json.Unmarshal([]byte(resp), &parsed)
	return parsed
}

func TestWebSocketClaimTask(t *testing.T) {
	hub, serverURL := setupWSHub(t)

	conn := connectAndRegister(t, serverURL, "claim-agent")
	defer conn.Close()

	// Create a task in the store
	task := &model.Task{Title: "WS claim test", Status: model.TaskReady}
	hub.store.CreateTask(task)

	// Claim via WS
	websocket.Message.Send(conn, `{"action":"claim","data":{"task_id":"`+task.ID+`"}}`)

	// Read the event broadcast
	evt := readWSMessage(t, conn)
	if evt["type"] != string(model.EventTaskClaimed) {
		t.Errorf("expected task_claimed event, got %v", evt["type"])
	}

	// Verify in store
	got, _ := hub.store.GetTask(task.ID)
	if got.Assignee != "claim-agent" {
		t.Errorf("expected claim-agent, got %s", got.Assignee)
	}
}

func TestWebSocketCompleteTask(t *testing.T) {
	hub, serverURL := setupWSHub(t)

	conn := connectAndRegister(t, serverURL, "complete-agent")
	defer conn.Close()

	task := &model.Task{Title: "WS complete test", Status: model.TaskInProgress, Assignee: "complete-agent"}
	hub.store.CreateTask(task)

	websocket.Message.Send(conn, `{"action":"complete","data":{"task_id":"`+task.ID+`"}}`)

	evt := readWSMessage(t, conn)
	if evt["type"] != string(model.EventTaskCompleted) {
		t.Errorf("expected task_completed event, got %v", evt["type"])
	}

	got, _ := hub.store.GetTask(task.ID)
	if got.Status != model.TaskDone {
		t.Errorf("expected done, got %s", got.Status)
	}
}

func TestWebSocketUnclaimTask(t *testing.T) {
	hub, serverURL := setupWSHub(t)

	conn := connectAndRegister(t, serverURL, "unclaim-agent")
	defer conn.Close()

	task := &model.Task{Title: "WS unclaim test", Status: model.TaskInProgress, Assignee: "unclaim-agent"}
	hub.store.CreateTask(task)

	websocket.Message.Send(conn, `{"action":"unclaim","data":{"task_id":"`+task.ID+`"}}`)

	evt := readWSMessage(t, conn)
	if evt["type"] != string(model.EventTaskUnclaimed) {
		t.Errorf("expected task_unclaimed event, got %v", evt["type"])
	}

	got, _ := hub.store.GetTask(task.ID)
	if got.Assignee != "" {
		t.Errorf("expected empty assignee, got %s", got.Assignee)
	}
}

func TestWebSocketSendMessage(t *testing.T) {
	hub, serverURL := setupWSHub(t)

	conn := connectAndRegister(t, serverURL, "msg-sender")
	defer conn.Close()

	websocket.Message.Send(conn, `{"action":"message","data":{"to":"other-agent","body":"hello via ws"}}`)

	// Read the event broadcast
	evt := readWSMessage(t, conn)
	if evt["type"] != string(model.EventMessage) {
		t.Errorf("expected message event, got %v", evt["type"])
	}

	// Verify in store
	msgs, _ := hub.store.ReadMessages("other-agent", 10)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Body != "hello via ws" {
		t.Errorf("expected 'hello via ws', got %s", msgs[0].Body)
	}
}

func TestWebSocketHeartbeat(t *testing.T) {
	hub, serverURL := setupWSHub(t)

	conn := connectAndRegister(t, serverURL, "heartbeat-agent")
	defer conn.Close()

	websocket.Message.Send(conn, `{"action":"heartbeat","data":{}}`)
	time.Sleep(50 * time.Millisecond)

	agent, err := hub.store.GetAgentByName("heartbeat-agent")
	if err != nil {
		t.Fatal(err)
	}
	if agent.Status != model.AgentConnected {
		t.Errorf("expected connected, got %s", agent.Status)
	}
}

func TestWebSocketClaimAlreadyClaimed(t *testing.T) {
	hub, serverURL := setupWSHub(t)

	conn := connectAndRegister(t, serverURL, "second-claimer")
	defer conn.Close()

	// Create a task already claimed by someone else
	task := &model.Task{Title: "Already claimed", Status: model.TaskInProgress, Assignee: "first-agent"}
	hub.store.CreateTask(task)

	websocket.Message.Send(conn, `{"action":"claim","data":{"task_id":"`+task.ID+`"}}`)

	evt := readWSMessage(t, conn)
	if evt["type"] != "error" {
		t.Errorf("expected error response, got %v", evt["type"])
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
