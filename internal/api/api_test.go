package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/maniginam/waggle/internal/event"
	"github.com/maniginam/waggle/internal/store"
)

func setup(t *testing.T) (*API, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	eh := event.NewHub()
	a := New(s, eh)
	ts := httptest.NewServer(a.Handler())
	t.Cleanup(ts.Close)
	return a, ts
}

func TestCreateAndListTasks(t *testing.T) {
	_, ts := setup(t)

	// Create
	body := `{"title":"Test task","priority":"high"}`
	resp, err := http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected 201, got %d", resp.StatusCode)
	}
	var task map[string]any
	json.NewDecoder(resp.Body).Decode(&task)
	if task["title"] != "Test task" {
		t.Errorf("expected 'Test task', got %v", task["title"])
	}
	if task["id"] == nil || task["id"] == "" {
		t.Error("expected task ID to be set")
	}

	// List
	resp2, _ := http.Get(ts.URL + "/api/tasks")
	defer resp2.Body.Close()
	var tasks []map[string]any
	json.NewDecoder(resp2.Body).Decode(&tasks)
	if len(tasks) != 1 {
		t.Errorf("expected 1 task, got %d", len(tasks))
	}
}

func TestCreateTaskRequiresTitle(t *testing.T) {
	_, ts := setup(t)
	resp, _ := http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(`{}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestGetTask(t *testing.T) {
	_, ts := setup(t)

	body := `{"title":"My task"}`
	resp, _ := http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(body))
	var created map[string]any
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	resp2, _ := http.Get(ts.URL + "/api/tasks/" + created["id"].(string))
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp2.StatusCode)
	}
}

func TestGetTaskNotFound(t *testing.T) {
	_, ts := setup(t)
	resp, _ := http.Get(ts.URL + "/api/tasks/nonexistent")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestUpdateTask(t *testing.T) {
	_, ts := setup(t)

	resp, _ := http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(`{"title":"Original"}`))
	var created map[string]any
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/tasks/"+created["id"].(string),
		bytes.NewBufferString(`{"title":"Updated"}`))
	req.Header.Set("Content-Type", "application/json")
	resp2, _ := http.DefaultClient.Do(req)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp2.StatusCode)
	}
	var updated map[string]any
	json.NewDecoder(resp2.Body).Decode(&updated)
	if updated["title"] != "Updated" {
		t.Errorf("expected Updated, got %v", updated["title"])
	}
}

func TestDeleteTask(t *testing.T) {
	_, ts := setup(t)

	resp, _ := http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(`{"title":"Delete me"}`))
	var created map[string]any
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/tasks/"+created["id"].(string), nil)
	resp2, _ := http.DefaultClient.Do(req)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", resp2.StatusCode)
	}
}

func TestClaimTask(t *testing.T) {
	_, ts := setup(t)

	// Create task
	resp, _ := http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(`{"title":"Claimable","status":"ready"}`))
	var task map[string]any
	json.NewDecoder(resp.Body).Decode(&task)
	resp.Body.Close()

	// Claim
	claimBody := `{"agent":"test-agent"}`
	resp2, _ := http.Post(ts.URL+"/api/tasks/"+task["id"].(string)+"/claim", "application/json", bytes.NewBufferString(claimBody))
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp2.StatusCode)
	}
}

func TestCompleteTask(t *testing.T) {
	_, ts := setup(t)

	resp, _ := http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(`{"title":"Complete me"}`))
	var task map[string]any
	json.NewDecoder(resp.Body).Decode(&task)
	resp.Body.Close()

	resp2, _ := http.Post(ts.URL+"/api/tasks/"+task["id"].(string)+"/complete", "application/json", nil)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp2.StatusCode)
	}
	var completed map[string]any
	json.NewDecoder(resp2.Body).Decode(&completed)
	if completed["status"] != "done" {
		t.Errorf("expected done, got %v", completed["status"])
	}
}

func TestListAgentsEmpty(t *testing.T) {
	_, ts := setup(t)
	resp, _ := http.Get(ts.URL + "/api/agents")
	defer resp.Body.Close()
	var agents []map[string]any
	json.NewDecoder(resp.Body).Decode(&agents)
	if len(agents) != 0 {
		t.Errorf("expected 0 agents, got %d", len(agents))
	}
}

func TestListEvents(t *testing.T) {
	_, ts := setup(t)

	// Create a task to generate an event
	http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(`{"title":"Event test"}`))

	resp, _ := http.Get(ts.URL + "/api/events")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestListTasksWithStatusFilter(t *testing.T) {
	_, ts := setup(t)

	http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(`{"title":"A","status":"ready"}`))
	http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(`{"title":"B","status":"backlog"}`))

	resp, _ := http.Get(ts.URL + "/api/tasks?status=ready")
	defer resp.Body.Close()
	var tasks []map[string]any
	json.NewDecoder(resp.Body).Decode(&tasks)
	if len(tasks) != 1 {
		t.Errorf("expected 1 ready task, got %d", len(tasks))
	}
}

func TestRegisterAgent(t *testing.T) {
	_, ts := setup(t)

	body := `{"name":"test-agent","type":"claude-code"}`
	resp, _ := http.Post(ts.URL+"/api/agents/register", "application/json", bytes.NewBufferString(body))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	var agent map[string]any
	json.NewDecoder(resp.Body).Decode(&agent)
	if agent["name"] != "test-agent" {
		t.Errorf("expected test-agent, got %v", agent["name"])
	}
	if agent["status"] != "connected" {
		t.Errorf("expected connected, got %v", agent["status"])
	}
}

func TestGetAgentByName(t *testing.T) {
	_, ts := setup(t)

	http.Post(ts.URL+"/api/agents/register", "application/json", bytes.NewBufferString(`{"name":"my-agent","type":"cursor"}`))

	resp, _ := http.Get(ts.URL + "/api/agents/my-agent")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	var agent map[string]any
	json.NewDecoder(resp.Body).Decode(&agent)
	if agent["name"] != "my-agent" {
		t.Errorf("expected my-agent, got %v", agent["name"])
	}
}

func TestSendAndReadMessages(t *testing.T) {
	_, ts := setup(t)

	// Send
	body := `{"from":"agent-1","to":"agent-2","body":"hello from API"}`
	resp, _ := http.Post(ts.URL+"/api/messages", "application/json", bytes.NewBufferString(body))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected 201, got %d", resp.StatusCode)
	}

	// Read
	resp2, _ := http.Get(ts.URL + "/api/messages?to=agent-2")
	defer resp2.Body.Close()
	var msgs []map[string]any
	json.NewDecoder(resp2.Body).Decode(&msgs)
	if len(msgs) != 1 {
		t.Errorf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0]["body"] != "hello from API" {
		t.Errorf("expected 'hello from API', got %v", msgs[0]["body"])
	}
}

func TestUpdateAgentStatus(t *testing.T) {
	_, ts := setup(t)

	http.Post(ts.URL+"/api/agents/register", "application/json", bytes.NewBufferString(`{"name":"status-agent","type":"aider"}`))

	body := `{"status":"working","current_task":"wg-123"}`
	resp, _ := http.Post(ts.URL+"/api/agents/status-agent/status", "application/json", bytes.NewBufferString(body))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestUnclaimTask(t *testing.T) {
	_, ts := setup(t)

	// Create and claim
	resp, _ := http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(`{"title":"Unclaim test","status":"ready"}`))
	var task map[string]any
	json.NewDecoder(resp.Body).Decode(&task)
	resp.Body.Close()

	http.Post(ts.URL+"/api/agents/register", "application/json", bytes.NewBufferString(`{"name":"unclaimer","type":"test"}`))
	http.Post(ts.URL+"/api/tasks/"+task["id"].(string)+"/claim", "application/json", bytes.NewBufferString(`{"agent":"unclaimer"}`))

	// Unclaim
	resp2, _ := http.Post(ts.URL+"/api/tasks/"+task["id"].(string)+"/unclaim", "application/json", bytes.NewBufferString(`{"agent":"unclaimer"}`))
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp2.StatusCode)
	}
	var unclaimed map[string]any
	json.NewDecoder(resp2.Body).Decode(&unclaimed)
	if unclaimed["status"] != "ready" {
		t.Errorf("expected ready after unclaim, got %v", unclaimed["status"])
	}
}

func TestCreateTaskInvalidStatus(t *testing.T) {
	_, ts := setup(t)
	resp, _ := http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(`{"title":"Bad","status":"invalid"}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid status, got %d", resp.StatusCode)
	}
}

func TestCreateTaskInvalidPriority(t *testing.T) {
	_, ts := setup(t)
	resp, _ := http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(`{"title":"Bad","priority":"urgent"}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid priority, got %d", resp.StatusCode)
	}
}

func TestDeleteInProgressTaskRejected(t *testing.T) {
	_, ts := setup(t)

	// Create and claim (makes it in_progress)
	resp, _ := http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(`{"title":"Busy task","status":"ready"}`))
	var task map[string]any
	json.NewDecoder(resp.Body).Decode(&task)
	resp.Body.Close()

	http.Post(ts.URL+"/api/agents/register", "application/json", bytes.NewBufferString(`{"name":"worker","type":"test"}`))
	http.Post(ts.URL+"/api/tasks/"+task["id"].(string)+"/claim", "application/json", bytes.NewBufferString(`{"agent":"worker"}`))

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/tasks/"+task["id"].(string), nil)
	resp2, _ := http.DefaultClient.Do(req)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Errorf("expected 409 for in-progress delete, got %d", resp2.StatusCode)
	}
}

func TestDoubleClaim(t *testing.T) {
	_, ts := setup(t)

	resp, _ := http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(`{"title":"Race test","status":"ready"}`))
	var task map[string]any
	json.NewDecoder(resp.Body).Decode(&task)
	resp.Body.Close()

	http.Post(ts.URL+"/api/agents/register", "application/json", bytes.NewBufferString(`{"name":"agent-a","type":"test"}`))
	http.Post(ts.URL+"/api/agents/register", "application/json", bytes.NewBufferString(`{"name":"agent-b","type":"test"}`))

	// First claim
	http.Post(ts.URL+"/api/tasks/"+task["id"].(string)+"/claim", "application/json", bytes.NewBufferString(`{"agent":"agent-a"}`))

	// Second claim should fail
	resp2, _ := http.Post(ts.URL+"/api/tasks/"+task["id"].(string)+"/claim", "application/json", bytes.NewBufferString(`{"agent":"agent-b"}`))
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Errorf("expected 409 for double claim, got %d", resp2.StatusCode)
	}
}

func TestUpdateTaskNotFound(t *testing.T) {
	_, ts := setup(t)
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/tasks/nonexistent",
		bytes.NewBufferString(`{"title":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestDeleteTaskNotFound(t *testing.T) {
	_, ts := setup(t)
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/tasks/nonexistent", nil)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestClaimTaskNotFound(t *testing.T) {
	_, ts := setup(t)
	resp, _ := http.Post(ts.URL+"/api/tasks/nonexistent/claim", "application/json",
		bytes.NewBufferString(`{"agent":"test"}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestCompleteTaskNotFound(t *testing.T) {
	_, ts := setup(t)
	resp, _ := http.Post(ts.URL+"/api/tasks/nonexistent/complete", "application/json", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestUnclaimWrongAgent(t *testing.T) {
	_, ts := setup(t)

	resp, _ := http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(`{"title":"Owned","status":"ready"}`))
	var task map[string]any
	json.NewDecoder(resp.Body).Decode(&task)
	resp.Body.Close()

	http.Post(ts.URL+"/api/agents/register", "application/json", bytes.NewBufferString(`{"name":"owner","type":"test"}`))
	http.Post(ts.URL+"/api/tasks/"+task["id"].(string)+"/claim", "application/json", bytes.NewBufferString(`{"agent":"owner"}`))

	// Try unclaim by wrong agent
	resp2, _ := http.Post(ts.URL+"/api/tasks/"+task["id"].(string)+"/unclaim", "application/json",
		bytes.NewBufferString(`{"agent":"thief"}`))
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 for wrong agent unclaim, got %d", resp2.StatusCode)
	}
}

func TestAgentNotFound(t *testing.T) {
	_, ts := setup(t)
	resp, _ := http.Get(ts.URL + "/api/agents/nonexistent")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestMessagesMissingTo(t *testing.T) {
	_, ts := setup(t)
	resp, _ := http.Get(ts.URL + "/api/messages")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for missing 'to', got %d", resp.StatusCode)
	}
}

func TestMessagesMissingFields(t *testing.T) {
	_, ts := setup(t)
	resp, _ := http.Post(ts.URL+"/api/messages", "application/json", bytes.NewBufferString(`{"body":"no from"}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for missing from, got %d", resp.StatusCode)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	_, ts := setup(t)

	// PUT on tasks should be 405
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/tasks", nil)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
}
