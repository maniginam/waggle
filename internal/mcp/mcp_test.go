package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
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

func TestToolsList(t *testing.T) {
	adapter, _ := setupMCP(t)
	resp := callMCP(t, adapter, "tools/list", 10, nil)

	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result map, got %v", resp)
	}
	tools, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("expected tools array, got %T", result["tools"])
	}

	expected := []string{
		"waggle_ctx_briefing", "waggle_park", "waggle_log", "waggle_whats_next",
		"waggle_create_task", "waggle_list_tasks", "waggle_update_task", "waggle_complete_task", "waggle_delete_task",
		"waggle_move_task", "waggle_create_sprint", "waggle_assign_sprint", "waggle_sprint_status",
		"waggle_list_projects", "waggle_update_project",
		"waggle_send_message", "waggle_read_messages",
	}

	if len(tools) != len(expected) {
		t.Errorf("expected %d tools, got %d", len(expected), len(tools))
	}

	names := map[string]bool{}
	for _, tool := range tools {
		tm := tool.(map[string]any)
		names[tm["name"].(string)] = true
	}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("missing tool: %s", name)
		}
	}
}

func TestCtxBriefing(t *testing.T) {
	adapter, ts := setupMCP(t)

	// Create a project first
	body := `{"name":"test-project","status":"active","health":"green"}`
	resp, _ := ts.Client().Post(ts.URL+"/api/projects", "application/json", strings.NewReader(body))
	var proj map[string]any
	json.NewDecoder(resp.Body).Decode(&proj)
	resp.Body.Close()
	projectID := proj["id"].(string)

	mcpResp := callMCP(t, adapter, "tools/call", 11, map[string]any{
		"name":      "waggle_ctx_briefing",
		"arguments": map[string]any{"project_id": projectID},
	})
	result := mcpResp["result"].(map[string]any)
	if result["isError"] != nil && result["isError"].(bool) {
		t.Fatalf("briefing failed: %v", result)
	}
	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "test-project") {
		t.Errorf("briefing should contain project name, got: %s", text[:100])
	}
}

func TestParkProject(t *testing.T) {
	adapter, ts := setupMCP(t)

	body := `{"name":"park-test","status":"active"}`
	resp, _ := ts.Client().Post(ts.URL+"/api/projects", "application/json", strings.NewReader(body))
	var proj map[string]any
	json.NewDecoder(resp.Body).Decode(&proj)
	resp.Body.Close()
	projectID := proj["id"].(string)

	mcpResp := callMCP(t, adapter, "tools/call", 12, map[string]any{
		"name": "waggle_park",
		"arguments": map[string]any{
			"project_id":   projectID,
			"parking_note": "stopped at feature X",
		},
	})
	result := mcpResp["result"].(map[string]any)
	if result["isError"] != nil && result["isError"].(bool) {
		t.Fatalf("park failed: %v", result)
	}

	// Verify parking note was set
	getResp, _ := ts.Client().Get(ts.URL + "/api/projects/" + projectID)
	var updated map[string]any
	json.NewDecoder(getResp.Body).Decode(&updated)
	getResp.Body.Close()
	if updated["parking_note"] != "stopped at feature X" {
		t.Errorf("expected parking note, got %v", updated["parking_note"])
	}
}

func TestLogProgress(t *testing.T) {
	adapter, ts := setupMCP(t)

	body := `{"name":"log-test","status":"active"}`
	resp, _ := ts.Client().Post(ts.URL+"/api/projects", "application/json", strings.NewReader(body))
	var proj map[string]any
	json.NewDecoder(resp.Body).Decode(&proj)
	resp.Body.Close()
	projectID := proj["id"].(string)

	mcpResp := callMCP(t, adapter, "tools/call", 13, map[string]any{
		"name": "waggle_log",
		"arguments": map[string]any{
			"project_id": projectID,
			"source":     "test",
			"summary":    "deployed v2",
		},
	})
	result := mcpResp["result"].(map[string]any)
	if result["isError"] != nil && result["isError"].(bool) {
		t.Fatalf("log failed: %v", result)
	}
}

func TestWhatsNext(t *testing.T) {
	adapter, ts := setupMCP(t)

	body := `{"name":"urgent-project","status":"active","health":"red"}`
	resp, _ := ts.Client().Post(ts.URL+"/api/projects", "application/json", strings.NewReader(body))
	resp.Body.Close()

	mcpResp := callMCP(t, adapter, "tools/call", 14, map[string]any{
		"name":      "waggle_whats_next",
		"arguments": map[string]any{},
	})
	result := mcpResp["result"].(map[string]any)
	if result["isError"] != nil && result["isError"].(bool) {
		t.Fatalf("whats_next failed: %v", result)
	}
	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "urgent-project") {
		t.Errorf("whats_next should contain urgent project, got: %s", text[:100])
	}
}

func TestMCPAgileTools(t *testing.T) {
	adapter, _ := setupMCP(t)
	// tools/list includes the new tools.
	list := callMCP(t, adapter, "tools/list", 1, nil)
	result, _ := list["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	names := map[string]bool{}
	for _, tv := range tools {
		if m, ok := tv.(map[string]any); ok {
			names[m["name"].(string)] = true
		}
	}
	for _, want := range []string{"waggle_move_task", "waggle_create_sprint", "waggle_assign_sprint", "waggle_sprint_status"} {
		if !names[want] {
			t.Errorf("missing tool %q", want)
		}
	}
	// create_sprint returns a sprint with an id.
	call := callMCP(t, adapter, "tools/call", 2, map[string]any{
		"name":      "waggle_create_sprint",
		"arguments": map[string]any{"project_id": "p1", "name": "S1"},
	})
	cr, _ := call["result"].(map[string]any)
	content, _ := cr["content"].([]any)
	if len(content) == 0 {
		t.Fatal("expected content")
	}
	text := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "\"id\"") {
		t.Errorf("expected sprint id in result, got %s", text)
	}
}

func TestMCPSprintStatusActive(t *testing.T) {
	adapter, ts := setupMCP(t)

	// Create a sprint (starts as 'planned').
	sResp, _ := ts.Client().Post(ts.URL+"/api/sprints", "application/json",
		strings.NewReader(`{"project_id":"p1","name":"S1"}`))
	var sprint map[string]any
	json.NewDecoder(sResp.Body).Decode(&sprint)
	sResp.Body.Close()
	sprintID := sprint["id"].(string)

	// Activate it.
	patchReq, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/sprints/"+sprintID,
		strings.NewReader(`{"state":"active"}`))
	patchReq.Header.Set("Content-Type", "application/json")
	pResp, _ := ts.Client().Do(patchReq)
	pResp.Body.Close()

	// Exercise the tools/call path for waggle_sprint_status.
	call := callMCP(t, adapter, "tools/call", 1, map[string]any{
		"name":      "waggle_sprint_status",
		"arguments": map[string]any{"project_id": "p1"},
	})
	result := call["result"].(map[string]any)
	if result["isError"] != nil && result["isError"].(bool) {
		t.Fatalf("sprint_status failed: %v", result)
	}
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)

	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("bad result JSON: %v (%s)", err, text)
	}
	active, ok := parsed["active_sprint"].(map[string]any)
	if !ok {
		t.Fatalf("expected active_sprint object, got %s", text)
	}
	if active["id"] != sprintID {
		t.Errorf("expected active sprint %s, got %v", sprintID, active["id"])
	}
	if _, present := parsed["burndown"]; !present {
		t.Errorf("expected burndown key in result, got %s", text)
	}
}

func TestSendAndReadMessages(t *testing.T) {
	adapter, _ := setupMCP(t)

	// Send
	sendResp := callMCP(t, adapter, "tools/call", 15, map[string]any{
		"name": "waggle_send_message",
		"arguments": map[string]any{
			"to":   "someone",
			"body": "hello from test",
		},
	})
	result := sendResp["result"].(map[string]any)
	if result["isError"] != nil && result["isError"].(bool) {
		t.Fatalf("send failed: %v", result)
	}

	// Read
	readResp := callMCP(t, adapter, "tools/call", 16, map[string]any{
		"name":      "waggle_read_messages",
		"arguments": map[string]any{"limit": 10},
	})
	result = readResp["result"].(map[string]any)
	if result["isError"] != nil && result["isError"].(bool) {
		t.Fatalf("read failed: %v", result)
	}
}

