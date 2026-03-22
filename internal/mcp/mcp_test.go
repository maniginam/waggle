package mcp

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maniginam/waggle/internal/api"
	"github.com/maniginam/waggle/internal/event"
	"github.com/maniginam/waggle/internal/store"
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
		"waggle_delete_task", "waggle_get_next_task",
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

func registeredAdapter(t *testing.T, ts *httptest.Server, name string) *Adapter {
	t.Helper()
	// Register via REST
	body := `{"name":"` + name + `","type":"test"}`
	resp, _ := ts.Client().Post(ts.URL+"/api/agents/register", "application/json", strings.NewReader(body))
	resp.Body.Close()
	a := NewAdapter(ts.URL)
	a.agentName = name
	return a
}

func createTaskViaREST(t *testing.T, ts *httptest.Server, title string) string {
	t.Helper()
	body := `{"title":"` + title + `","status":"ready"}`
	resp, _ := ts.Client().Post(ts.URL+"/api/tasks", "application/json", strings.NewReader(body))
	var task map[string]any
	json.NewDecoder(resp.Body).Decode(&task)
	resp.Body.Close()
	return task["id"].(string)
}

func TestShowTask(t *testing.T) {
	adapter, ts := setupMCP(t)
	taskID := createTaskViaREST(t, ts, "Show me")

	resp := callMCP(t, adapter, "tools/call", 10, map[string]any{
		"name":      "waggle_show_task",
		"arguments": map[string]any{"id": taskID},
	})
	result := resp["result"].(map[string]any)
	if result["isError"] != nil && result["isError"].(bool) {
		t.Fatalf("show task failed: %v", result)
	}
}

func TestUpdateTask(t *testing.T) {
	adapter, ts := setupMCP(t)
	taskID := createTaskViaREST(t, ts, "Update me")

	resp := callMCP(t, adapter, "tools/call", 11, map[string]any{
		"name":      "waggle_update_task",
		"arguments": map[string]any{"id": taskID, "priority": "critical"},
	})
	result := resp["result"].(map[string]any)
	if result["isError"] != nil && result["isError"].(bool) {
		t.Fatalf("update task failed: %v", result)
	}
}

func TestClaimAndUnclaim(t *testing.T) {
	_, ts := setupMCP(t)
	adapter := registeredAdapter(t, ts, "claim-agent")
	taskID := createTaskViaREST(t, ts, "Claim test")

	// Claim
	resp := callMCP(t, adapter, "tools/call", 12, map[string]any{
		"name":      "waggle_claim_task",
		"arguments": map[string]any{"id": taskID},
	})
	result := resp["result"].(map[string]any)
	if result["isError"] != nil && result["isError"].(bool) {
		t.Fatalf("claim failed: %v", result)
	}

	// Unclaim
	adapter2 := registeredAdapter(t, ts, "claim-agent")
	resp2 := callMCP(t, adapter2, "tools/call", 13, map[string]any{
		"name":      "waggle_unclaim_task",
		"arguments": map[string]any{"id": taskID},
	})
	result2 := resp2["result"].(map[string]any)
	if result2["isError"] != nil && result2["isError"].(bool) {
		t.Fatalf("unclaim failed: %v", result2)
	}
}

func TestCompleteTask(t *testing.T) {
	_, ts := setupMCP(t)
	adapter := registeredAdapter(t, ts, "completer")
	taskID := createTaskViaREST(t, ts, "Complete me")

	resp := callMCP(t, adapter, "tools/call", 14, map[string]any{
		"name":      "waggle_complete_task",
		"arguments": map[string]any{"id": taskID},
	})
	result := resp["result"].(map[string]any)
	if result["isError"] != nil && result["isError"].(bool) {
		t.Fatalf("complete failed: %v", result)
	}
}

func TestListAgents(t *testing.T) {
	_, ts := setupMCP(t)
	adapter := registeredAdapter(t, ts, "list-agent")

	resp := callMCP(t, adapter, "tools/call", 15, map[string]any{
		"name":      "waggle_list_agents",
		"arguments": map[string]any{},
	})
	result := resp["result"].(map[string]any)
	if result["isError"] != nil && result["isError"].(bool) {
		t.Fatalf("list agents failed: %v", result)
	}
}

func TestSetStatus(t *testing.T) {
	_, ts := setupMCP(t)
	adapter := registeredAdapter(t, ts, "status-agent")

	resp := callMCP(t, adapter, "tools/call", 16, map[string]any{
		"name":      "waggle_set_status",
		"arguments": map[string]any{"status": "working", "current_task": "wg-123"},
	})
	result := resp["result"].(map[string]any)
	if result["isError"] != nil && result["isError"].(bool) {
		t.Fatalf("set status failed: %v", result)
	}
}

func TestSendAndReadMessages(t *testing.T) {
	_, ts := setupMCP(t)
	sender := registeredAdapter(t, ts, "sender-agent")

	// Send
	resp := callMCP(t, sender, "tools/call", 17, map[string]any{
		"name":      "waggle_send_message",
		"arguments": map[string]any{"to": "recipient", "body": "hello from mcp"},
	})
	result := resp["result"].(map[string]any)
	if result["isError"] != nil && result["isError"].(bool) {
		t.Fatalf("send message failed: %v", result)
	}

	// Read
	reader := registeredAdapter(t, ts, "recipient")
	resp2 := callMCP(t, reader, "tools/call", 18, map[string]any{
		"name":      "waggle_read_messages",
		"arguments": map[string]any{"limit": 10},
	})
	result2 := resp2["result"].(map[string]any)
	if result2["isError"] != nil && result2["isError"].(bool) {
		t.Fatalf("read messages failed: %v", result2)
	}
}

func TestUnknownTool(t *testing.T) {
	adapter, _ := setupMCP(t)
	resp := callMCP(t, adapter, "tools/call", 19, map[string]any{
		"name":      "waggle_nonexistent",
		"arguments": map[string]any{},
	})
	result := resp["result"].(map[string]any)
	if result["isError"] == nil || !result["isError"].(bool) {
		t.Error("expected error for unknown tool")
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

func TestDeleteTask(t *testing.T) {
	adapter, ts := setupMCP(t)
	taskID := createTaskViaREST(t, ts, "Delete me")

	resp := callMCP(t, adapter, "tools/call", 20, map[string]any{
		"name":      "waggle_delete_task",
		"arguments": map[string]any{"id": taskID},
	})
	result := resp["result"].(map[string]any)
	if result["isError"] != nil && result["isError"].(bool) {
		t.Fatalf("delete task failed: %v", result)
	}

	// Verify it's gone
	resp2 := callMCP(t, adapter, "tools/call", 21, map[string]any{
		"name":      "waggle_show_task",
		"arguments": map[string]any{"id": taskID},
	})
	result2 := resp2["result"].(map[string]any)
	if result2["isError"] == nil || !result2["isError"].(bool) {
		t.Error("expected error showing deleted task")
	}
}

func TestGetNextTask(t *testing.T) {
	_, ts := setupMCP(t)
	adapter := registeredAdapter(t, ts, "next-agent")

	// Create tasks with different priorities via REST
	for _, body := range []string{
		`{"title":"Low task","priority":"low","status":"ready"}`,
		`{"title":"Critical task","priority":"critical","status":"ready"}`,
		`{"title":"High task","priority":"high","status":"ready"}`,
	} {
		resp, _ := ts.Client().Post(ts.URL+"/api/tasks", "application/json", strings.NewReader(body))
		resp.Body.Close()
	}

	resp := callMCP(t, adapter, "tools/call", 22, map[string]any{
		"name":      "waggle_get_next_task",
		"arguments": map[string]any{},
	})
	result := resp["result"].(map[string]any)
	if result["isError"] != nil && result["isError"].(bool) {
		t.Fatalf("get next task failed: %v", result)
	}

	// Parse the content text to verify it's the critical task
	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	var task map[string]any
	json.Unmarshal([]byte(text), &task)
	if task["title"] != "Critical task" {
		t.Errorf("expected critical task, got %v", task["title"])
	}
}

func TestGetNextTaskEmpty(t *testing.T) {
	adapter, _ := setupMCP(t)

	resp := callMCP(t, adapter, "tools/call", 23, map[string]any{
		"name":      "waggle_get_next_task",
		"arguments": map[string]any{},
	})
	result := resp["result"].(map[string]any)
	if result["isError"] != nil && result["isError"].(bool) {
		t.Fatalf("get next task failed: %v", result)
	}
	// Should return "no ready tasks" message
	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "No ready tasks") {
		t.Errorf("expected 'No ready tasks' message, got %s", text)
	}
}

func TestCommentsViaMCP(t *testing.T) {
	_, ts := setupMCP(t)
	adapter := registeredAdapter(t, ts, "commenter")
	taskID := createTaskViaREST(t, ts, "Comment target")

	// Add comment
	resp := callMCP(t, adapter, "tools/call", 25, map[string]any{
		"name":      "waggle_add_comment",
		"arguments": map[string]any{"id": taskID, "body": "Making progress"},
	})
	result := resp["result"].(map[string]any)
	if result["isError"] != nil && result["isError"].(bool) {
		t.Fatalf("add comment failed: %v", result)
	}

	// List comments
	adapter2 := registeredAdapter(t, ts, "commenter")
	resp2 := callMCP(t, adapter2, "tools/call", 26, map[string]any{
		"name":      "waggle_list_comments",
		"arguments": map[string]any{"id": taskID},
	})
	result2 := resp2["result"].(map[string]any)
	if result2["isError"] != nil && result2["isError"].(bool) {
		t.Fatalf("list comments failed: %v", result2)
	}
	content := result2["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	var comments []any
	json.Unmarshal([]byte(text), &comments)
	if len(comments) != 1 {
		t.Errorf("expected 1 comment, got %d", len(comments))
	}
}

func TestSearchViaMCP(t *testing.T) {
	adapter, ts := setupMCP(t)

	// Create tasks
	for _, title := range []string{"Build auth module", "Fix login bug", "Write tests"} {
		body := `{"title":"` + title + `"}`
		resp, _ := ts.Client().Post(ts.URL+"/api/tasks", "application/json", strings.NewReader(body))
		resp.Body.Close()
	}

	resp := callMCP(t, adapter, "tools/call", 24, map[string]any{
		"name":      "waggle_list_tasks",
		"arguments": map[string]any{"q": "auth"},
	})
	result := resp["result"].(map[string]any)
	if result["isError"] != nil && result["isError"].(bool) {
		t.Fatalf("search failed: %v", result)
	}
	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	var tasks []any
	json.Unmarshal([]byte(text), &tasks)
	if len(tasks) != 1 {
		t.Errorf("expected 1 task matching 'auth', got %d", len(tasks))
	}
}
