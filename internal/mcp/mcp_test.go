package mcp

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cleancoders-studio/waggle/internal/api"
	"github.com/cleancoders-studio/waggle/internal/event"
	"github.com/cleancoders-studio/waggle/internal/store"
)

func setupMCP(t *testing.T) (*Adapter, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	eh := event.NewHub()
	a := api.New(s, eh)
	ts := httptest.NewServer(a.Handler())
	t.Cleanup(ts.Close)

	adapter := NewAdapter(ts.URL)
	return adapter, ts
}

func callMCP(t *testing.T, adapter *Adapter, method string, id any, params any) map[string]any {
	t.Helper()
	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		b, _ := json.Marshal(params)
		req["params"] = json.RawMessage(b)
	}
	line, _ := json.Marshal(req)

	var out bytes.Buffer
	adapter.in = strings.NewReader(string(line) + "\n")
	adapter.out = &out
	adapter.Run()

	var resp map[string]any
	json.Unmarshal(out.Bytes(), &resp)
	return resp
}

func TestInitialize(t *testing.T) {
	adapter, _ := setupMCP(t)
	resp := callMCP(t, adapter, "initialize", 1, map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "1.0"},
	})

	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result map, got %T: %v", resp["result"], resp)
	}
	if result["protocolVersion"] != protocolVersion {
		t.Errorf("expected %s, got %v", protocolVersion, result["protocolVersion"])
	}
	serverInfo, ok := result["serverInfo"].(map[string]any)
	if !ok {
		t.Fatal("missing serverInfo")
	}
	if serverInfo["name"] != "waggle" {
		t.Errorf("expected waggle, got %v", serverInfo["name"])
	}
}

func TestToolsList(t *testing.T) {
	adapter, _ := setupMCP(t)
	resp := callMCP(t, adapter, "tools/list", 2, nil)

	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result map, got %v", resp)
	}
	tools, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("expected tools array, got %T", result["tools"])
	}
	if len(tools) < 10 {
		t.Errorf("expected at least 10 tools, got %d", len(tools))
	}

	// Verify expected tool names
	names := map[string]bool{}
	for _, tool := range tools {
		m := tool.(map[string]any)
		names[m["name"].(string)] = true
	}
	expected := []string{
		"waggle_register_agent", "waggle_create_task", "waggle_list_tasks",
		"waggle_show_task", "waggle_claim_task", "waggle_complete_task",
		"waggle_send_message", "waggle_read_messages",
	}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("missing tool: %s", name)
		}
	}
}

func TestRegisterAndCreateTask(t *testing.T) {
	adapter, _ := setupMCP(t)

	// Register agent
	resp := callMCP(t, adapter, "tools/call", 3, map[string]any{
		"name":      "waggle_register_agent",
		"arguments": map[string]any{"name": "test-agent", "type": "claude-code"},
	})
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result, got %v", resp)
	}
	if result["isError"] != nil && result["isError"].(bool) {
		t.Fatalf("register failed: %v", result)
	}

	// Create task — need a fresh adapter with state preserved, so create inline
	adapter2 := NewAdapter(adapter.baseURL)
	adapter2.agentName = "test-agent"

	resp2 := callMCP(t, adapter2, "tools/call", 4, map[string]any{
		"name":      "waggle_create_task",
		"arguments": map[string]any{"title": "MCP test task", "priority": "high"},
	})
	result2, ok := resp2["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result, got %v", resp2)
	}
	if result2["isError"] != nil && result2["isError"].(bool) {
		t.Fatalf("create task failed: %v", result2)
	}
}

func TestListTasks(t *testing.T) {
	adapter, ts := setupMCP(t)

	// Create a task via REST first
	body := `{"title":"Listed task"}`
	resp, _ := ts.Client().Post(ts.URL+"/api/tasks", "application/json", strings.NewReader(body))
	resp.Body.Close()

	// List via MCP
	mcpResp := callMCP(t, adapter, "tools/call", 5, map[string]any{
		"name":      "waggle_list_tasks",
		"arguments": map[string]any{},
	})
	result := mcpResp["result"].(map[string]any)
	if result["isError"] != nil && result["isError"].(bool) {
		t.Fatalf("list tasks failed: %v", result)
	}
	content := result["content"].([]any)
	if len(content) == 0 {
		t.Error("expected content in response")
	}
}

func TestPing(t *testing.T) {
	adapter, _ := setupMCP(t)
	resp := callMCP(t, adapter, "ping", 6, nil)
	if resp["result"] == nil {
		t.Error("expected result for ping")
	}
}

func TestUnknownMethod(t *testing.T) {
	adapter, _ := setupMCP(t)
	resp := callMCP(t, adapter, "unknown/method", 7, nil)
	if resp["error"] == nil {
		t.Error("expected error for unknown method")
	}
}

func TestClaimWithoutRegister(t *testing.T) {
	adapter, ts := setupMCP(t)

	// Create a task first
	body := `{"title":"Claim test"}`
	resp, _ := ts.Client().Post(ts.URL+"/api/tasks", "application/json", strings.NewReader(body))
	var task map[string]any
	json.NewDecoder(resp.Body).Decode(&task)
	resp.Body.Close()

	// Try to claim without registering
	mcpResp := callMCP(t, adapter, "tools/call", 8, map[string]any{
		"name":      "waggle_claim_task",
		"arguments": map[string]any{"id": task["id"]},
	})
	result := mcpResp["result"].(map[string]any)
	if result["isError"] == nil || !result["isError"].(bool) {
		t.Error("expected error when claiming without registration")
	}
}
