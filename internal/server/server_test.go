package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func startTestServer(t *testing.T) (string, *Server) {
	t.Helper()
	dir := t.TempDir()
	srv, err := New(Config{
		Port:   0, // Will be overridden
		DBPath: filepath.Join(dir, "test.db"),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Find a free port by starting on 0
	// Actually, use a fixed high port for testing
	port := 14740 + time.Now().Nanosecond()%1000
	srv.httpServer.Addr = fmt.Sprintf(":%d", port)

	go srv.Start()
	t.Cleanup(func() {
		srv.Shutdown(t.Context())
	})

	// Wait for server to be ready
	base := fmt.Sprintf("http://localhost:%d", port)
	for i := 0; i < 50; i++ {
		time.Sleep(50 * time.Millisecond)
		if resp, err := http.Get(base + "/health"); err == nil {
			resp.Body.Close()
			break
		}
	}

	return base, srv
}

func TestServerHealthCheck(t *testing.T) {
	base, _ := startTestServer(t)

	resp, err := http.Get(base + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestServerFullWorkflow(t *testing.T) {
	base, _ := startTestServer(t)

	// Register agent
	resp, _ := http.Post(base+"/api/agents/register", "application/json",
		strings.NewReader(`{"name":"workflow-agent","type":"claude-code"}`))
	resp.Body.Close()

	// Create task
	resp, _ = http.Post(base+"/api/tasks", "application/json",
		strings.NewReader(`{"title":"Server test","priority":"high","criteria":["integration passes"]}`))
	var task map[string]any
	json.NewDecoder(resp.Body).Decode(&task)
	resp.Body.Close()
	taskID := task["id"].(string)

	// Claim task
	resp, _ = http.Post(base+"/api/tasks/"+taskID+"/claim", "application/json",
		strings.NewReader(`{"agent":"workflow-agent"}`))
	resp.Body.Close()

	// Verify agent is working
	resp, _ = http.Get(base + "/api/agents/workflow-agent")
	var agent map[string]any
	json.NewDecoder(resp.Body).Decode(&agent)
	resp.Body.Close()
	if agent["current_task"] != taskID {
		t.Errorf("expected agent working on %s, got %v", taskID, agent["current_task"])
	}

	// Complete task
	resp, _ = http.Post(base+"/api/tasks/"+taskID+"/complete", "application/json", nil)
	var completed map[string]any
	json.NewDecoder(resp.Body).Decode(&completed)
	resp.Body.Close()
	if completed["status"] != "done" {
		t.Errorf("expected done, got %v", completed["status"])
	}

	// Verify events were recorded
	resp, _ = http.Get(base + "/api/events")
	var events []map[string]any
	json.NewDecoder(resp.Body).Decode(&events)
	resp.Body.Close()
	if len(events) < 3 {
		t.Errorf("expected at least 3 events (register, create, claim, complete), got %d", len(events))
	}
}

func TestServerCORS(t *testing.T) {
	base, _ := startTestServer(t)

	req, _ := http.NewRequest(http.MethodOptions, base+"/api/tasks", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204 for OPTIONS, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Error("expected CORS header")
	}
}
