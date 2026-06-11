package server

import (
	"encoding/json"
	"fmt"
	"net"
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
		Port:    0, // Will be overridden
		DBPath:  filepath.Join(dir, "test.db"),
		Version: "test-v0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Find a free port by binding to :0
	ln, err2 := net.Listen("tcp", ":0")
	if err2 != nil {
		t.Fatal(err2)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
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
	var health map[string]any
	json.NewDecoder(resp.Body).Decode(&health)
	if health["status"] != "ok" {
		t.Errorf("expected status ok, got %v", health["status"])
	}
	if health["version"] != "test-v0.0.1" {
		t.Errorf("expected version test-v0.0.1, got %v", health["version"])
	}
	if health["uptime"] == nil {
		t.Error("expected uptime in health response")
	}
	if health["started_at"] == nil {
		t.Error("expected started_at in health response")
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
	if resp.StatusCode >= 400 {
		t.Fatalf("claim failed with status %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Verify agent is working
	resp, _ = http.Get(base + "/api/agents/workflow-agent")
	var agent map[string]any
	json.NewDecoder(resp.Body).Decode(&agent)
	resp.Body.Close()
	if agent["current_task"] != taskID {
		t.Errorf("expected agent working on %s, got %v", taskID, agent["current_task"])
	}

	// Complete task — retry once if server is still processing the claim
	resp, _ = http.Post(base+"/api/tasks/"+taskID+"/complete", "application/json", nil)
	if resp.StatusCode >= 500 {
		resp.Body.Close()
		time.Sleep(100 * time.Millisecond)
		resp, _ = http.Post(base+"/api/tasks/"+taskID+"/complete", "application/json", nil)
	}
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

func TestAgentHeartbeatLifecycle(t *testing.T) {
	base, srv := startTestServer(t)

	// 1. Register agent — should be connected
	resp, _ := http.Post(base+"/api/agents/register", "application/json",
		strings.NewReader(`{"name":"lifecycle-agent","type":"claude-code"}`))
	var agent map[string]any
	json.NewDecoder(resp.Body).Decode(&agent)
	resp.Body.Close()

	if agent["status"] != "connected" {
		t.Fatalf("expected connected after register, got %v", agent["status"])
	}
	firstSeen := agent["last_seen"].(string)

	// Create and claim a task so we can verify it's released on disconnect
	resp, _ = http.Post(base+"/api/tasks", "application/json",
		strings.NewReader(`{"title":"Lifecycle task","status":"ready"}`))
	var task map[string]any
	json.NewDecoder(resp.Body).Decode(&task)
	resp.Body.Close()
	taskID := task["id"].(string)

	resp, _ = http.Post(base+"/api/tasks/"+taskID+"/claim", "application/json",
		strings.NewReader(`{"agent":"lifecycle-agent"}`))
	resp.Body.Close()

	// 2. Heartbeat updates last_seen — POST status=connected acts as heartbeat
	// Sleep >1s because last_seen timestamps use RFC3339 (second precision)
	time.Sleep(1100 * time.Millisecond)
	resp, _ = http.Post(base+"/api/agents/lifecycle-agent/status", "application/json",
		strings.NewReader(`{"status":"connected"}`))
	resp.Body.Close()

	resp, _ = http.Get(base + "/api/agents/lifecycle-agent")
	var afterHeartbeat map[string]any
	json.NewDecoder(resp.Body).Decode(&afterHeartbeat)
	resp.Body.Close()

	if afterHeartbeat["last_seen"] == firstSeen {
		t.Error("expected last_seen to update after heartbeat")
	}

	// 3 & 4. Stale detection: reap agents whose last_seen is before now+1s
	// (simulates 90s elapsing without a heartbeat)
	cutoff := time.Now().UTC().Add(time.Second)
	srv.reapAgentsStaleBefore(cutoff)

	resp, _ = http.Get(base + "/api/agents/lifecycle-agent")
	var reaped map[string]any
	json.NewDecoder(resp.Body).Decode(&reaped)
	resp.Body.Close()

	if reaped["status"] != "disconnected" {
		t.Errorf("expected disconnected after stale reap, got %v", reaped["status"])
	}

	// Verify the claimed task was released back to ready
	resp, _ = http.Get(base + "/api/tasks/" + taskID)
	var releasedTask map[string]any
	json.NewDecoder(resp.Body).Decode(&releasedTask)
	resp.Body.Close()

	if releasedTask["status"] != "ready" {
		t.Errorf("expected task status ready after agent disconnect, got %v", releasedTask["status"])
	}
	if releasedTask["assignee"] != nil && releasedTask["assignee"] != "" {
		t.Errorf("expected task assignee cleared after disconnect, got %v", releasedTask["assignee"])
	}
}

func TestStaleAgentEmitsEvent(t *testing.T) {
	base, srv := startTestServer(t)

	// Register an agent
	resp, _ := http.Post(base+"/api/agents/register", "application/json",
		strings.NewReader(`{"name":"stale-test-agent","type":"claude-code"}`))
	resp.Body.Close()

	// Subscribe to events to capture the stale event
	sub := srv.EventHub().Subscribe("", "")
	defer srv.EventHub().Unsubscribe(sub)

	// Reap with a future cutoff so the agent appears stale
	cutoff := time.Now().UTC().Add(time.Second)
	srv.reapAgentsStaleBefore(cutoff)

	// Should receive an agent_stale event followed by agent_left
	gotStale := false
	deadline := time.After(2 * time.Second)
	for {
		select {
		case evt := <-sub.Ch:
			if evt.Type == "agent_stale" && evt.AgentID == "stale-test-agent" {
				gotStale = true
			}
		case <-deadline:
			goto done
		}
		if gotStale {
			break
		}
	}
done:
	if !gotStale {
		t.Error("expected agent_stale event to be emitted when agent goes stale")
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

func TestServerVersion(t *testing.T) {
	base, _ := startTestServer(t)

	resp, err := http.Get(base + "/api/version")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	if result["version"] != "test-v0.0.1" {
		t.Errorf("expected version test-v0.0.1, got %s", result["version"])
	}
}

func TestServerStoreAccessor(t *testing.T) {
	_, srv := startTestServer(t)
	if srv.Store() == nil {
		t.Error("expected non-nil store")
	}
}

func TestReapSkipsDisconnectedAgents(t *testing.T) {
	base, srv := startTestServer(t)

	// Register and disconnect an agent
	resp, _ := http.Post(base+"/api/agents/register", "application/json",
		strings.NewReader(`{"name":"already-disconnected","type":"claude-code"}`))
	resp.Body.Close()

	resp, _ = http.Post(base+"/api/agents/already-disconnected/status", "application/json",
		strings.NewReader(`{"status":"disconnected"}`))
	resp.Body.Close()

	// Reap with future cutoff — should skip already disconnected agents
	cutoff := time.Now().UTC().Add(time.Second)
	srv.reapAgentsStaleBefore(cutoff)

	resp, _ = http.Get(base + "/api/agents/already-disconnected")
	var agent map[string]any
	json.NewDecoder(resp.Body).Decode(&agent)
	resp.Body.Close()

	if agent["status"] != "disconnected" {
		t.Errorf("expected still disconnected, got %v", agent["status"])
	}
}
