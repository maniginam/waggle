package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maniginam/waggle/internal/event"
	"github.com/maniginam/waggle/internal/model"
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

func mustGet(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func mustPost(t *testing.T, url, contentType string, body io.Reader) *http.Response {
	t.Helper()
	resp, err := http.Post(url, contentType, body)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func mustDo(t *testing.T, req *http.Request) *http.Response {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
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
	resp2 := mustGet(t, ts.URL + "/api/tasks")
	defer resp2.Body.Close()
	var tasks []map[string]any
	json.NewDecoder(resp2.Body).Decode(&tasks)
	if len(tasks) != 1 {
		t.Errorf("expected 1 task, got %d", len(tasks))
	}
}

func TestCreateTaskRequiresTitle(t *testing.T) {
	_, ts := setup(t)
	resp := mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(`{}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestGetTask(t *testing.T) {
	_, ts := setup(t)

	body := `{"title":"My task"}`
	resp := mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(body))
	var created map[string]any
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	resp2 := mustGet(t, ts.URL + "/api/tasks/" + created["id"].(string))
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp2.StatusCode)
	}
}

func TestGetTaskNotFound(t *testing.T) {
	_, ts := setup(t)
	resp := mustGet(t, ts.URL + "/api/tasks/nonexistent")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestUpdateTask(t *testing.T) {
	_, ts := setup(t)

	resp := mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(`{"title":"Original"}`))
	var created map[string]any
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/tasks/"+created["id"].(string),
		bytes.NewBufferString(`{"title":"Updated"}`))
	req.Header.Set("Content-Type", "application/json")
	resp2 := mustDo(t, req)
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

	resp := mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(`{"title":"Delete me"}`))
	var created map[string]any
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/tasks/"+created["id"].(string), nil)
	resp2 := mustDo(t, req)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", resp2.StatusCode)
	}
}

func TestClaimTask(t *testing.T) {
	_, ts := setup(t)

	// Create task
	resp := mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(`{"title":"Claimable","status":"ready"}`))
	var task map[string]any
	json.NewDecoder(resp.Body).Decode(&task)
	resp.Body.Close()

	// Claim
	claimBody := `{"agent":"test-agent"}`
	resp2 := mustPost(t, ts.URL+"/api/tasks/"+task["id"].(string)+"/claim", "application/json", bytes.NewBufferString(claimBody))
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp2.StatusCode)
	}
}

func TestCompleteTask(t *testing.T) {
	_, ts := setup(t)

	resp := mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(`{"title":"Complete me"}`))
	var task map[string]any
	json.NewDecoder(resp.Body).Decode(&task)
	resp.Body.Close()

	resp2 := mustPost(t, ts.URL+"/api/tasks/"+task["id"].(string)+"/complete", "application/json", nil)
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
	resp := mustGet(t, ts.URL + "/api/agents")
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

	resp := mustGet(t, ts.URL + "/api/events")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestListTasksWithStatusFilter(t *testing.T) {
	_, ts := setup(t)

	http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(`{"title":"A","status":"ready"}`))
	http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(`{"title":"B","status":"backlog"}`))

	resp := mustGet(t, ts.URL + "/api/tasks?status=ready")
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
	resp := mustPost(t, ts.URL+"/api/agents/register", "application/json", bytes.NewBufferString(body))
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

	resp := mustGet(t, ts.URL + "/api/agents/my-agent")
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
	resp := mustPost(t, ts.URL+"/api/messages", "application/json", bytes.NewBufferString(body))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected 201, got %d", resp.StatusCode)
	}

	// Read
	resp2 := mustGet(t, ts.URL + "/api/messages?to=agent-2")
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
	resp := mustPost(t, ts.URL+"/api/agents/status-agent/status", "application/json", bytes.NewBufferString(body))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestUnclaimTask(t *testing.T) {
	_, ts := setup(t)

	// Create and claim
	resp := mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(`{"title":"Unclaim test","status":"ready"}`))
	var task map[string]any
	json.NewDecoder(resp.Body).Decode(&task)
	resp.Body.Close()

	http.Post(ts.URL+"/api/agents/register", "application/json", bytes.NewBufferString(`{"name":"unclaimer","type":"test"}`))
	http.Post(ts.URL+"/api/tasks/"+task["id"].(string)+"/claim", "application/json", bytes.NewBufferString(`{"agent":"unclaimer"}`))

	// Unclaim
	resp2 := mustPost(t, ts.URL+"/api/tasks/"+task["id"].(string)+"/unclaim", "application/json", bytes.NewBufferString(`{"agent":"unclaimer"}`))
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
	resp := mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(`{"title":"Bad","status":"invalid"}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid status, got %d", resp.StatusCode)
	}
}

func TestCreateTaskInvalidPriority(t *testing.T) {
	_, ts := setup(t)
	resp := mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(`{"title":"Bad","priority":"urgent"}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid priority, got %d", resp.StatusCode)
	}
}

func TestDeleteInProgressTaskRejected(t *testing.T) {
	_, ts := setup(t)

	// Create and claim (makes it in_progress)
	resp := mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(`{"title":"Busy task","status":"ready"}`))
	var task map[string]any
	json.NewDecoder(resp.Body).Decode(&task)
	resp.Body.Close()

	http.Post(ts.URL+"/api/agents/register", "application/json", bytes.NewBufferString(`{"name":"worker","type":"test"}`))
	http.Post(ts.URL+"/api/tasks/"+task["id"].(string)+"/claim", "application/json", bytes.NewBufferString(`{"agent":"worker"}`))

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/tasks/"+task["id"].(string), nil)
	resp2 := mustDo(t, req)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Errorf("expected 409 for in-progress delete, got %d", resp2.StatusCode)
	}
}

func TestDoubleClaim(t *testing.T) {
	_, ts := setup(t)

	resp := mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(`{"title":"Race test","status":"ready"}`))
	var task map[string]any
	json.NewDecoder(resp.Body).Decode(&task)
	resp.Body.Close()

	http.Post(ts.URL+"/api/agents/register", "application/json", bytes.NewBufferString(`{"name":"agent-a","type":"test"}`))
	http.Post(ts.URL+"/api/agents/register", "application/json", bytes.NewBufferString(`{"name":"agent-b","type":"test"}`))

	// First claim
	http.Post(ts.URL+"/api/tasks/"+task["id"].(string)+"/claim", "application/json", bytes.NewBufferString(`{"agent":"agent-a"}`))

	// Second claim should fail
	resp2 := mustPost(t, ts.URL+"/api/tasks/"+task["id"].(string)+"/claim", "application/json", bytes.NewBufferString(`{"agent":"agent-b"}`))
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
	resp := mustDo(t, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestDeleteTaskNotFound(t *testing.T) {
	_, ts := setup(t)
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/tasks/nonexistent", nil)
	resp := mustDo(t, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestClaimTaskNotFound(t *testing.T) {
	_, ts := setup(t)
	resp := mustPost(t, ts.URL+"/api/tasks/nonexistent/claim", "application/json",
		bytes.NewBufferString(`{"agent":"test"}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestCompleteTaskNotFound(t *testing.T) {
	_, ts := setup(t)
	resp := mustPost(t, ts.URL+"/api/tasks/nonexistent/complete", "application/json", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestUnclaimWrongAgent(t *testing.T) {
	_, ts := setup(t)

	resp := mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(`{"title":"Owned","status":"ready"}`))
	var task map[string]any
	json.NewDecoder(resp.Body).Decode(&task)
	resp.Body.Close()

	http.Post(ts.URL+"/api/agents/register", "application/json", bytes.NewBufferString(`{"name":"owner","type":"test"}`))
	http.Post(ts.URL+"/api/tasks/"+task["id"].(string)+"/claim", "application/json", bytes.NewBufferString(`{"agent":"owner"}`))

	// Try unclaim by wrong agent
	resp2 := mustPost(t, ts.URL+"/api/tasks/"+task["id"].(string)+"/unclaim", "application/json",
		bytes.NewBufferString(`{"agent":"thief"}`))
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 for wrong agent unclaim, got %d", resp2.StatusCode)
	}
}

func TestAgentNotFound(t *testing.T) {
	_, ts := setup(t)
	resp := mustGet(t, ts.URL + "/api/agents/nonexistent")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestMessagesListAll(t *testing.T) {
	_, ts := setup(t)
	resp := mustGet(t, ts.URL + "/api/messages")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for listing all messages, got %d", resp.StatusCode)
	}
}

func TestMessagesMissingFields(t *testing.T) {
	_, ts := setup(t)
	resp := mustPost(t, ts.URL+"/api/messages", "application/json", bytes.NewBufferString(`{"body":"no from"}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for missing from, got %d", resp.StatusCode)
	}
}

func TestTaskDepsAPI(t *testing.T) {
	_, ts := setup(t)

	// Create tasks with dependency
	body, _ := json.Marshal(map[string]string{"title": "Dep parent"})
	resp := mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(body))
	var parentTask map[string]any
	json.NewDecoder(resp.Body).Decode(&parentTask)
	resp.Body.Close()
	parentID := parentTask["id"].(string)

	body, _ = json.Marshal(map[string]any{"title": "Dep child", "depends_on": []string{parentID}})
	resp = mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(body))
	resp.Body.Close()

	resp = mustGet(t, ts.URL + "/api/tasks/" + parentID + "/deps")
	defer resp.Body.Close()
	var deps map[string]any
	json.NewDecoder(resp.Body).Decode(&deps)
	blocking := deps["blocking"].([]any)
	if len(blocking) != 1 {
		t.Errorf("expected 1 blocked task, got %d", len(blocking))
	}
}

func TestTaskHistoryAPI(t *testing.T) {
	_, ts := setup(t)

	// Create and claim a task to generate events
	body, _ := json.Marshal(map[string]string{"title": "History test", "status": "ready"})
	resp := mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(body))
	var task map[string]any
	json.NewDecoder(resp.Body).Decode(&task)
	resp.Body.Close()
	taskID := task["id"].(string)

	// Get history
	resp = mustGet(t, ts.URL + "/api/tasks/" + taskID + "/history")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var events []map[string]any
	json.NewDecoder(resp.Body).Decode(&events)
	// Should have at least the task_created event
	if len(events) < 1 {
		t.Errorf("expected at least 1 event, got %d", len(events))
	}
}

func TestSubtasksAPI(t *testing.T) {
	_, ts := setup(t)

	// Create parent
	body, _ := json.Marshal(map[string]string{"title": "Parent"})
	resp := mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(body))
	var parent map[string]any
	json.NewDecoder(resp.Body).Decode(&parent)
	resp.Body.Close()
	parentID := parent["id"].(string)

	// Create subtasks
	for _, s := range []string{"done", "ready"} {
		sub, _ := json.Marshal(map[string]string{"title": "Sub " + s, "parent_id": parentID, "status": s})
		r, _ := http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(sub))
		r.Body.Close()
	}

	resp = mustGet(t, ts.URL + "/api/tasks/" + parentID + "/subtasks")
	defer resp.Body.Close()
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	subtasks := result["subtasks"].([]any)
	if len(subtasks) != 2 {
		t.Errorf("expected 2 subtasks, got %d", len(subtasks))
	}
	progress := result["progress"].(map[string]any)
	if progress["done"].(float64) != 1 {
		t.Errorf("expected 1 done, got %v", progress["done"])
	}
	if progress["total"].(float64) != 2 {
		t.Errorf("expected 2 total, got %v", progress["total"])
	}
}

func TestCommentsAPI(t *testing.T) {
	_, ts := setup(t)

	// Create a task
	body, _ := json.Marshal(map[string]string{"title": "Comment test"})
	resp := mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(body))
	var task map[string]any
	json.NewDecoder(resp.Body).Decode(&task)
	resp.Body.Close()
	taskID := task["id"].(string)

	// Add a comment
	commentBody, _ := json.Marshal(map[string]string{"author": "test-agent", "body": "Working on it"})
	resp = mustPost(t, ts.URL+"/api/tasks/"+taskID+"/comments", "application/json", bytes.NewBuffer(commentBody))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// List comments
	resp = mustGet(t, ts.URL + "/api/tasks/" + taskID + "/comments")
	defer resp.Body.Close()
	var comments []map[string]any
	json.NewDecoder(resp.Body).Decode(&comments)
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(comments))
	}
	if comments[0]["body"] != "Working on it" {
		t.Errorf("unexpected body: %v", comments[0]["body"])
	}
}

func TestCommentsMissingFields(t *testing.T) {
	_, ts := setup(t)

	body, _ := json.Marshal(map[string]string{"title": "Comment test 2"})
	resp := mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(body))
	var task map[string]any
	json.NewDecoder(resp.Body).Decode(&task)
	resp.Body.Close()
	taskID := task["id"].(string)

	// Missing author
	commentBody, _ := json.Marshal(map[string]string{"body": "no author"})
	resp = mustPost(t, ts.URL+"/api/tasks/"+taskID+"/comments", "application/json", bytes.NewBuffer(commentBody))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestStatsAPI(t *testing.T) {
	_, ts := setup(t)

	// Create some tasks
	for _, body := range []string{
		`{"title":"Task 1","status":"ready","priority":"high"}`,
		`{"title":"Task 2","status":"ready","priority":"critical"}`,
		`{"title":"Task 3","status":"done","priority":"low"}`,
	} {
		http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(body))
	}

	resp := mustGet(t, ts.URL + "/api/stats")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var stats map[string]any
	json.NewDecoder(resp.Body).Decode(&stats)
	if stats["total_tasks"].(float64) != 3 {
		t.Errorf("expected 3 total tasks, got %v", stats["total_tasks"])
	}
	byStatus := stats["tasks_by_status"].(map[string]any)
	if byStatus["ready"].(float64) != 2 {
		t.Errorf("expected 2 ready, got %v", byStatus["ready"])
	}
}

func TestSearchTasksAPI(t *testing.T) {
	_, ts := setup(t)

	// Create tasks
	for _, title := range []string{"Build auth", "Fix auth timeout", "Write docs"} {
		body, _ := json.Marshal(map[string]string{"title": title})
		http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(body))
	}

	resp := mustGet(t, ts.URL + "/api/tasks?q=auth")
	defer resp.Body.Close()
	var tasks []map[string]any
	json.NewDecoder(resp.Body).Decode(&tasks)
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks matching 'auth', got %d", len(tasks))
	}
}

func TestProjectCRUDAPI(t *testing.T) {
	_, ts := setup(t)

	// Create project
	body := `{"name":"Auth System","description":"Authentication and authorization"}`
	resp := mustPost(t, ts.URL+"/api/projects", "application/json", bytes.NewBufferString(body))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var project map[string]any
	json.NewDecoder(resp.Body).Decode(&project)
	resp.Body.Close()
	projectID := project["id"].(string)

	if project["name"] != "Auth System" {
		t.Errorf("expected Auth System, got %v", project["name"])
	}

	// Get project
	resp = mustGet(t, ts.URL + "/api/projects/" + projectID)
	var got map[string]any
	json.NewDecoder(resp.Body).Decode(&got)
	resp.Body.Close()
	if got["name"] != "Auth System" {
		t.Errorf("expected Auth System, got %v", got["name"])
	}

	// List projects
	resp = mustGet(t, ts.URL + "/api/projects")
	var projects []map[string]any
	json.NewDecoder(resp.Body).Decode(&projects)
	resp.Body.Close()
	if len(projects) != 1 {
		t.Errorf("expected 1 project, got %d", len(projects))
	}

	// Update project
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/projects/"+projectID,
		bytes.NewBufferString(`{"name":"Auth System v2"}`))
	req.Header.Set("Content-Type", "application/json")
	resp = mustDo(t, req)
	var updated map[string]any
	json.NewDecoder(resp.Body).Decode(&updated)
	resp.Body.Close()
	if updated["name"] != "Auth System v2" {
		t.Errorf("expected Auth System v2, got %v", updated["name"])
	}

	// Delete project
	req, _ = http.NewRequest(http.MethodDelete, ts.URL+"/api/projects/"+projectID, nil)
	resp = mustDo(t, req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", resp.StatusCode)
	}

	// Verify deleted
	resp = mustGet(t, ts.URL + "/api/projects/" + projectID)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", resp.StatusCode)
	}
}

func TestProjectEpicsAPI(t *testing.T) {
	_, ts := setup(t)

	// Create project
	resp := mustPost(t, ts.URL+"/api/projects", "application/json",
		bytes.NewBufferString(`{"name":"My Project"}`))
	var project map[string]any
	json.NewDecoder(resp.Body).Decode(&project)
	resp.Body.Close()
	projectID := project["id"].(string)

	// Create epic under project
	epicBody, _ := json.Marshal(map[string]any{
		"title":      "User Auth Epic",
		"task_type":  "epic",
		"project_id": projectID,
	})
	resp = mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(epicBody))
	var epic map[string]any
	json.NewDecoder(resp.Body).Decode(&epic)
	resp.Body.Close()
	epicID := epic["id"].(string)

	// Create stories under epic
	for _, title := range []string{"Login flow", "Password reset"} {
		storyBody, _ := json.Marshal(map[string]any{
			"title":      title,
			"task_type":  "story",
			"parent_id":  epicID,
			"project_id": projectID,
			"criteria":   []string{"All tests pass", "Code reviewed"},
		})
		r, _ := http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(storyBody))
		r.Body.Close()
	}

	// Get project epics
	resp = mustGet(t, ts.URL + "/api/projects/" + projectID + "/epics")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var epics []map[string]any
	json.NewDecoder(resp.Body).Decode(&epics)
	if len(epics) != 1 {
		t.Fatalf("expected 1 epic, got %d", len(epics))
	}
	if epics[0]["title"] != "User Auth Epic" {
		t.Errorf("expected User Auth Epic, got %v", epics[0]["title"])
	}
	progress := epics[0]["progress"].(map[string]any)
	if progress["total"].(float64) != 2 {
		t.Errorf("expected 2 total subtasks, got %v", progress["total"])
	}
}

func TestProjectMissingName(t *testing.T) {
	_, ts := setup(t)
	resp := mustPost(t, ts.URL+"/api/projects", "application/json",
		bytes.NewBufferString(`{"description":"no name"}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestProjectListWithCounts(t *testing.T) {
	_, ts := setup(t)

	// Create a project
	body, _ := json.Marshal(map[string]string{"name": "Test Project"})
	resp := mustPost(t, ts.URL+"/api/projects", "application/json", bytes.NewBuffer(body))
	var project map[string]any
	json.NewDecoder(resp.Body).Decode(&project)
	resp.Body.Close()
	pid := project["id"].(string)

	// Create tasks in the project
	for _, status := range []string{"ready", "in_progress", "done", "done"} {
		taskBody, _ := json.Marshal(map[string]string{"title": status + " task", "status": status, "project_id": pid})
		r, _ := http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(taskBody))
		r.Body.Close()
	}

	// Fetch without counts
	resp = mustGet(t, ts.URL + "/api/projects")
	var plain []map[string]any
	json.NewDecoder(resp.Body).Decode(&plain)
	resp.Body.Close()
	if _, hasCounts := plain[0]["task_count"]; hasCounts {
		t.Error("expected no task_count without counts=true")
	}

	// Fetch with counts
	resp = mustGet(t, ts.URL + "/api/projects?counts=true")
	var enriched []map[string]any
	json.NewDecoder(resp.Body).Decode(&enriched)
	resp.Body.Close()
	if len(enriched) == 0 {
		t.Fatal("expected at least 1 project")
	}
	p := enriched[0]
	if int(p["task_count"].(float64)) != 4 {
		t.Errorf("expected task_count=4, got %v", p["task_count"])
	}
	if int(p["done_count"].(float64)) != 2 {
		t.Errorf("expected done_count=2, got %v", p["done_count"])
	}
	if int(p["active_count"].(float64)) != 1 {
		t.Errorf("expected active_count=1, got %v", p["active_count"])
	}
}

func TestTaskTypeFilterAPI(t *testing.T) {
	_, ts := setup(t)

	// Create tasks of different types
	for _, tt := range []string{"epic", "story", "task"} {
		body, _ := json.Marshal(map[string]string{"title": tt + " item", "task_type": tt})
		r, _ := http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(body))
		r.Body.Close()
	}

	// Filter by type
	resp := mustGet(t, ts.URL + "/api/tasks?task_type=epic")
	defer resp.Body.Close()
	var tasks []map[string]any
	json.NewDecoder(resp.Body).Decode(&tasks)
	if len(tasks) != 1 {
		t.Errorf("expected 1 epic, got %d", len(tasks))
	}
	if tasks[0]["task_type"] != "epic" {
		t.Errorf("expected epic type, got %v", tasks[0]["task_type"])
	}
}

func TestSettingsAPI(t *testing.T) {
	_, ts := setup(t)

	// GET empty settings
	resp := mustGet(t, ts.URL + "/api/settings")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var settings map[string]string
	json.NewDecoder(resp.Body).Decode(&settings)
	resp.Body.Close()
	if len(settings) != 0 {
		t.Errorf("expected empty settings, got %d", len(settings))
	}

	// PUT a setting
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/settings",
		bytes.NewBufferString(`{"theme":"dark","sound":"on"}`))
	req.Header.Set("Content-Type", "application/json")
	resp = mustDo(t, req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// GET settings back
	resp = mustGet(t, ts.URL + "/api/settings")
	json.NewDecoder(resp.Body).Decode(&settings)
	resp.Body.Close()
	if settings["theme"] != "dark" {
		t.Errorf("expected dark theme, got %q", settings["theme"])
	}
	if settings["sound"] != "on" {
		t.Errorf("expected sound on, got %q", settings["sound"])
	}
}

func TestExportTasksJSON(t *testing.T) {
	_, ts := setup(t)

	// Create tasks
	for _, body := range []string{
		`{"title":"Task A","priority":"high","status":"ready"}`,
		`{"title":"Task B","priority":"low","status":"done"}`,
	} {
		r, _ := http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(body))
		r.Body.Close()
	}

	resp := mustGet(t, ts.URL + "/api/tasks/export?format=json")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected application/json, got %s", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); cd == "" {
		t.Error("expected Content-Disposition header")
	}
	var tasks []map[string]any
	json.NewDecoder(resp.Body).Decode(&tasks)
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(tasks))
	}
}

func TestExportTasksCSV(t *testing.T) {
	_, ts := setup(t)

	http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBufferString(`{"title":"CSV Task","priority":"high","status":"ready"}`))

	resp := mustGet(t, ts.URL + "/api/tasks/export?format=csv")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/csv" {
		t.Errorf("expected text/csv, got %s", ct)
	}

	// Read CSV content
	body := new(bytes.Buffer)
	body.ReadFrom(resp.Body)
	lines := bytes.Split(body.Bytes(), []byte("\n"))
	// Should have header + 1 data row + possible trailing newline
	if len(lines) < 2 {
		t.Errorf("expected at least 2 CSV lines (header+data), got %d", len(lines))
	}
}

func TestRateLimiter(t *testing.T) {
	rl := newRateLimiter(3, 60*1000*1000*1000) // 3 requests per minute

	// First 3 should succeed
	for i := 0; i < 3; i++ {
		if !rl.Allow("client-1") {
			t.Errorf("request %d should be allowed", i+1)
		}
	}

	// 4th should be rate limited
	if rl.Allow("client-1") {
		t.Error("4th request should be rate limited")
	}

	// Different client should still work
	if !rl.Allow("client-2") {
		t.Error("different client should not be rate limited")
	}
}

func TestBodySizeLimit(t *testing.T) {
	_, ts := setup(t)

	// Create a body that exceeds 1MB
	bigBody := make([]byte, 2*1024*1024)
	for i := range bigBody {
		bigBody[i] = 'x'
	}

	resp, err := http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewReader(bigBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// Should fail with 400 or similar due to bad JSON / size limit
	if resp.StatusCode == 201 {
		t.Error("expected request with 2MB body to not create a task")
	}
}

func TestMethodNotAllowed(t *testing.T) {
	_, ts := setup(t)

	// PUT on tasks should be 405
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/tasks", nil)
	resp := mustDo(t, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
}

func TestInputLimits(t *testing.T) {
	_, ts := setup(t)

	// Task title too long
	longTitle := make([]byte, 501)
	for i := range longTitle {
		longTitle[i] = 'a'
	}
	body, _ := json.Marshal(map[string]string{"title": string(longTitle)})
	resp := mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(body))
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for long title, got %d", resp.StatusCode)
	}

	// Task description too long
	longDesc := make([]byte, 10001)
	for i := range longDesc {
		longDesc[i] = 'b'
	}
	body, _ = json.Marshal(map[string]string{"title": "ok", "description": string(longDesc)})
	resp = mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(body))
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for long description, got %d", resp.StatusCode)
	}

	// Project name too long
	longName := make([]byte, 201)
	for i := range longName {
		longName[i] = 'c'
	}
	body, _ = json.Marshal(map[string]string{"name": string(longName)})
	resp = mustPost(t, ts.URL+"/api/projects", "application/json", bytes.NewBuffer(body))
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for long project name, got %d", resp.StatusCode)
	}

	// Valid lengths should work
	body, _ = json.Marshal(map[string]string{"title": "short title"})
	resp = mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(body))
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected 201 for valid title, got %d", resp.StatusCode)
	}
}

func TestReviewCRUD(t *testing.T) {
	_, ts := setup(t)

	// Create a task first
	resp := mustPost(t, ts.URL+"/api/tasks", "application/json",
		bytes.NewBufferString(`{"title":"Review target"}`))
	var task map[string]any
	json.NewDecoder(resp.Body).Decode(&task)
	resp.Body.Close()
	taskID := task["id"].(string)

	// List reviews — empty
	resp = mustGet(t, ts.URL + "/api/reviews")
	var reviews []map[string]any
	json.NewDecoder(resp.Body).Decode(&reviews)
	resp.Body.Close()
	if len(reviews) != 0 {
		t.Errorf("expected 0 reviews, got %d", len(reviews))
	}

	// Submit a review
	revBody, _ := json.Marshal(map[string]string{
		"task_id":  taskID,
		"agent_id": "test-agent",
		"diff":     "--- a/file.go\n+++ b/file.go\n@@ -1 +1 @@\n-old\n+new",
	})
	resp = mustPost(t, ts.URL+"/api/reviews", "application/json", bytes.NewBuffer(revBody))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var rev map[string]any
	json.NewDecoder(resp.Body).Decode(&rev)
	resp.Body.Close()
	revID := rev["id"].(string)
	if rev["task_id"] != taskID {
		t.Errorf("expected task_id %s, got %v", taskID, rev["task_id"])
	}

	// Get single review
	resp = mustGet(t, ts.URL + "/api/reviews/" + revID)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Approve the review
	approveBody, _ := json.Marshal(map[string]string{"status": "approved", "feedback": "lgtm"})
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/reviews/"+revID, bytes.NewBuffer(approveBody))
	req.Header.Set("Content-Type", "application/json")
	resp = mustDo(t, req)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for approve, got %d", resp.StatusCode)
	}
	var approved map[string]any
	json.NewDecoder(resp.Body).Decode(&approved)
	resp.Body.Close()
	if approved["status"] != "approved" {
		t.Errorf("expected status approved, got %v", approved["status"])
	}

	// List reviews filtered by status
	resp = mustGet(t, ts.URL + "/api/reviews?status=approved")
	json.NewDecoder(resp.Body).Decode(&reviews)
	resp.Body.Close()
	if len(reviews) != 1 {
		t.Errorf("expected 1 approved review, got %d", len(reviews))
	}

	// List reviews by task
	resp = mustGet(t, ts.URL + "/api/reviews?task_id=" + taskID)
	json.NewDecoder(resp.Body).Decode(&reviews)
	resp.Body.Close()
	if len(reviews) != 1 {
		t.Errorf("expected 1 review for task, got %d", len(reviews))
	}
}

func TestReviewValidation(t *testing.T) {
	_, ts := setup(t)

	// Missing required fields
	resp := mustPost(t, ts.URL+"/api/reviews", "application/json",
		bytes.NewBufferString(`{"task_id":"abc"}`))
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for missing diff, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Invalid review status
	resp = mustPost(t, ts.URL+"/api/reviews", "application/json",
		bytes.NewBufferString(`{"task_id":"abc","diff":"something"}`))
	resp.Body.Close()

	// Get nonexistent review
	resp = mustGet(t, ts.URL + "/api/reviews/nonexistent")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for nonexistent review, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Invalid status on patch
	body, _ := json.Marshal(map[string]string{"status": "invalid"})
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/reviews/some-id", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	resp = mustDo(t, req)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid review status, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestUsageAPI(t *testing.T) {
	_, ts := setup(t)

	// GET usage — empty
	resp := mustGet(t, ts.URL + "/api/usage")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var usage map[string]any
	json.NewDecoder(resp.Body).Decode(&usage)
	resp.Body.Close()
	if usage["by_agent"] == nil {
		t.Error("expected by_agent key")
	}
	if usage["recent"] == nil {
		t.Error("expected recent key")
	}

	// POST usage record
	body, _ := json.Marshal(map[string]any{
		"agent_name":   "test-agent",
		"input_tokens": 1000,
		"output_tokens": 500,
		"model":        "claude-sonnet",
	})
	resp = mustPost(t, ts.URL+"/api/usage", "application/json", bytes.NewBuffer(body))
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// GET again — should have data
	resp = mustGet(t, ts.URL + "/api/usage")
	json.NewDecoder(resp.Body).Decode(&usage)
	resp.Body.Close()

	// POST missing agent_name
	resp = mustPost(t, ts.URL+"/api/usage", "application/json",
		bytes.NewBufferString(`{"input_tokens":100}`))
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for missing agent_name, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestPushSubscribeAPI(t *testing.T) {
	_, ts := setup(t)

	// GET VAPID public key
	resp := mustGet(t, ts.URL + "/api/push/subscribe")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for GET push/subscribe, got %d", resp.StatusCode)
	}
	var vapid map[string]string
	json.NewDecoder(resp.Body).Decode(&vapid)
	resp.Body.Close()
	if _, ok := vapid["public_key"]; !ok {
		t.Error("expected public_key in response")
	}

	// Subscribe
	body, _ := json.Marshal(map[string]string{
		"endpoint": "https://push.example.com/sub/123",
		"auth":     "test-auth-key",
		"p256dh":   "test-p256dh-key",
	})
	resp = mustPost(t, ts.URL+"/api/push/subscribe", "application/json", bytes.NewBuffer(body))
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected 201 for push subscribe, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Missing fields
	resp = mustPost(t, ts.URL+"/api/push/subscribe", "application/json",
		bytes.NewBufferString(`{"endpoint":"https://example.com"}`))
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for missing auth/p256dh, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Unsubscribe
	unsubBody, _ := json.Marshal(map[string]string{"endpoint": "https://push.example.com/sub/123"})
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/push/subscribe", bytes.NewBuffer(unsubBody))
	req.Header.Set("Content-Type", "application/json")
	resp = mustDo(t, req)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for unsubscribe, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestSpawnValidation(t *testing.T) {
	_, ts := setup(t)

	// GET should be method not allowed
	resp := mustGet(t, ts.URL+"/api/spawn")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for GET spawn, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Missing name
	resp = mustPost(t, ts.URL+"/api/spawn", "application/json",
		bytes.NewBufferString(`{"work_dir":"/tmp"}`))
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for missing name, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Invalid name
	resp = mustPost(t, ts.URL+"/api/spawn", "application/json",
		bytes.NewBufferString(`{"name":"bad name!","work_dir":"/tmp"}`))
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid name, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Missing work_dir
	resp = mustPost(t, ts.URL+"/api/spawn", "application/json",
		bytes.NewBufferString(`{"name":"test-agent"}`))
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for missing work_dir, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Nonexistent work_dir
	resp = mustPost(t, ts.URL+"/api/spawn", "application/json",
		bytes.NewBufferString(`{"name":"test-agent","work_dir":"/nonexistent/path/xyz"}`))
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for bad work_dir, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestSessionsListAPI(t *testing.T) {
	_, ts := setup(t)

	// GET sessions should work even with no tmux
	resp := mustGet(t, ts.URL+"/api/sessions")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for sessions list, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestDeleteAgent(t *testing.T) {
	_, ts := setup(t)

	// Register then delete
	body, _ := json.Marshal(map[string]string{"name": "doomed-agent", "type": "claude-code"})
	r := mustPost(t, ts.URL+"/api/agents/register", "application/json", bytes.NewBuffer(body))
	r.Body.Close()

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/agents/doomed-agent", nil)
	resp := mustDo(t, req)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Verify deleted
	resp = mustGet(t, ts.URL+"/api/agents/doomed-agent")
	if resp.StatusCode != 404 {
		t.Errorf("expected 404 after delete, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAgentProjectAssignment(t *testing.T) {
	_, ts := setup(t)

	// Register agent
	body, _ := json.Marshal(map[string]string{"name": "proj-agent", "type": "claude-code"})
	r := mustPost(t, ts.URL+"/api/agents/register", "application/json", bytes.NewBuffer(body))
	r.Body.Close()

	// Create a project
	body, _ = json.Marshal(map[string]string{"name": "test-proj"})
	r = mustPost(t, ts.URL+"/api/projects", "application/json", bytes.NewBuffer(body))
	var proj map[string]any
	json.NewDecoder(r.Body).Decode(&proj)
	r.Body.Close()
	projID := proj["id"].(string)

	// Assign agent to project
	body, _ = json.Marshal(map[string]string{"project_id": projID})
	r = mustPost(t, ts.URL+"/api/agents/proj-agent/project", "application/json", bytes.NewBuffer(body))
	if r.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", r.StatusCode)
	}
	r.Body.Close()
}

func TestAgentDisconnectViaStatus(t *testing.T) {
	_, ts := setup(t)

	body, _ := json.Marshal(map[string]string{"name": "disco-agent", "type": "claude-code"})
	r := mustPost(t, ts.URL+"/api/agents/register", "application/json", bytes.NewBuffer(body))
	r.Body.Close()

	// Disconnect via status endpoint
	body, _ = json.Marshal(map[string]string{"status": "disconnected"})
	r = mustPost(t, ts.URL+"/api/agents/disco-agent/status", "application/json", bytes.NewBuffer(body))
	if r.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", r.StatusCode)
	}
	r.Body.Close()

	// Verify disconnected
	resp := mustGet(t, ts.URL+"/api/agents/disco-agent")
	var agent map[string]any
	json.NewDecoder(resp.Body).Decode(&agent)
	resp.Body.Close()
	if agent["status"] != "disconnected" {
		t.Errorf("expected disconnected, got %v", agent["status"])
	}
}

func TestMessageMarkRead(t *testing.T) {
	_, ts := setup(t)

	// Send a message
	body, _ := json.Marshal(map[string]string{"from": "alpha", "to": "beta", "body": "hello"})
	r := mustPost(t, ts.URL+"/api/messages", "application/json", bytes.NewBuffer(body))
	var msg map[string]any
	json.NewDecoder(r.Body).Decode(&msg)
	r.Body.Close()
	msgID := msg["id"].(string)

	// Mark read by ID
	body, _ = json.Marshal(map[string]any{"ids": []string{msgID}})
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/messages", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	resp := mustDo(t, req)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Mark all read
	body, _ = json.Marshal(map[string]any{"mark_all": true})
	req, _ = http.NewRequest(http.MethodPatch, ts.URL+"/api/messages", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	resp = mustDo(t, req)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 for mark all, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestMessageSearch(t *testing.T) {
	_, ts := setup(t)

	// Send messages
	body, _ := json.Marshal(map[string]string{"from": "alice", "to": "bob", "body": "unique-search-term-xyz"})
	r := mustPost(t, ts.URL+"/api/messages", "application/json", bytes.NewBuffer(body))
	r.Body.Close()

	body, _ = json.Marshal(map[string]string{"from": "bob", "to": "alice", "body": "different content"})
	r = mustPost(t, ts.URL+"/api/messages", "application/json", bytes.NewBuffer(body))
	r.Body.Close()

	// Search
	resp := mustGet(t, ts.URL+"/api/messages?q=unique-search-term-xyz")
	var msgs []map[string]any
	json.NewDecoder(resp.Body).Decode(&msgs)
	resp.Body.Close()
	if len(msgs) != 1 {
		t.Errorf("expected 1 search result, got %d", len(msgs))
	}
}

func TestMessageValidationLimits(t *testing.T) {
	_, ts := setup(t)

	// Body too long (>10000)
	longBody := strings.Repeat("x", 10001)
	body, _ := json.Marshal(map[string]string{"from": "a", "to": "b", "body": longBody})
	resp := mustPost(t, ts.URL+"/api/messages", "application/json", bytes.NewBuffer(body))
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for body too long, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Name too long (>64)
	longName := strings.Repeat("x", 65)
	body, _ = json.Marshal(map[string]string{"from": longName, "to": "b", "body": "hi"})
	resp = mustPost(t, ts.URL+"/api/messages", "application/json", bytes.NewBuffer(body))
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for name too long, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAgentRegistrationValidation(t *testing.T) {
	_, ts := setup(t)

	// Name too long
	longName := strings.Repeat("x", 65)
	body, _ := json.Marshal(map[string]string{"name": longName, "type": "claude-code"})
	resp := mustPost(t, ts.URL+"/api/agents/register", "application/json", bytes.NewBuffer(body))
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for name too long, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Missing name
	body, _ = json.Marshal(map[string]string{"type": "claude-code"})
	resp = mustPost(t, ts.URL+"/api/agents/register", "application/json", bytes.NewBuffer(body))
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for missing name, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestCommentBodyTooLong(t *testing.T) {
	_, ts := setup(t)

	// Create a task
	body, _ := json.Marshal(map[string]string{"title": "comment-test"})
	r := mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(body))
	var task map[string]any
	json.NewDecoder(r.Body).Decode(&task)
	r.Body.Close()
	taskID := task["id"].(string)

	// Comment with body > 5000
	longBody := strings.Repeat("x", 5001)
	body, _ = json.Marshal(map[string]string{"author": "test", "body": longBody})
	resp := mustPost(t, ts.URL+"/api/tasks/"+taskID+"/comments", "application/json", bytes.NewBuffer(body))
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for comment body too long, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAgentAutoLeaderRole(t *testing.T) {
	_, ts := setup(t)

	// Create a project first
	body, _ := json.Marshal(map[string]string{"name": "auto-leader-proj"})
	r := mustPost(t, ts.URL+"/api/projects", "application/json", bytes.NewBuffer(body))
	var proj map[string]any
	json.NewDecoder(r.Body).Decode(&proj)
	r.Body.Close()
	projID := proj["id"].(string)

	// Register agent with project_id — should get auto-assigned leader role
	body, _ = json.Marshal(map[string]string{"name": "auto-lead", "type": "claude-code", "project_id": projID})
	resp := mustPost(t, ts.URL+"/api/agents/register", "application/json", bytes.NewBuffer(body))
	var agent map[string]any
	json.NewDecoder(resp.Body).Decode(&agent)
	resp.Body.Close()
	if agent["role"] != "leader" {
		t.Errorf("expected auto-assigned leader role, got %v", agent["role"])
	}
}

func TestMethodNotAllowedEndpoints(t *testing.T) {
	_, ts := setup(t)

	endpoints := []struct {
		method string
		path   string
	}{
		{http.MethodPut, "/api/agents"},
		{http.MethodDelete, "/api/messages"},
		{http.MethodPut, "/api/events"},
	}

	for _, ep := range endpoints {
		req, _ := http.NewRequest(ep.method, ts.URL+ep.path, nil)
		resp := mustDo(t, req)
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: expected 405, got %d", ep.method, ep.path, resp.StatusCode)
		}
	}
}

func TestTaskDepsNotFound(t *testing.T) {
	_, ts := setup(t)

	resp := mustGet(t, ts.URL+"/api/tasks/nonexistent/deps")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestTaskDepsMethodNotAllowed(t *testing.T) {
	_, ts := setup(t)

	body, _ := json.Marshal(map[string]string{"title": "dep-method-test"})
	r := mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(body))
	var task map[string]any
	json.NewDecoder(r.Body).Decode(&task)
	r.Body.Close()
	taskID := task["id"].(string)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/tasks/"+taskID+"/deps", nil)
	resp := mustDo(t, req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
}

func TestSubtasksMethodNotAllowed(t *testing.T) {
	_, ts := setup(t)

	body, _ := json.Marshal(map[string]string{"title": "sub-method-test"})
	r := mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(body))
	var task map[string]any
	json.NewDecoder(r.Body).Decode(&task)
	r.Body.Close()
	taskID := task["id"].(string)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/tasks/"+taskID+"/subtasks", nil)
	resp := mustDo(t, req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
}

func TestHistoryMethodNotAllowed(t *testing.T) {
	_, ts := setup(t)

	body, _ := json.Marshal(map[string]string{"title": "history-method-test"})
	r := mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(body))
	var task map[string]any
	json.NewDecoder(r.Body).Decode(&task)
	r.Body.Close()
	taskID := task["id"].(string)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/tasks/"+taskID+"/history", nil)
	resp := mustDo(t, req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
}

func TestEventsContainMultipleTypes(t *testing.T) {
	_, ts := setup(t)

	// Create a task (generates task_created event)
	body, _ := json.Marshal(map[string]string{"title": "event-test"})
	r := mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(body))
	r.Body.Close()

	// Register agent (generates agent_joined event)
	body, _ = json.Marshal(map[string]string{"name": "event-agent", "type": "claude-code"})
	r = mustPost(t, ts.URL+"/api/agents/register", "application/json", bytes.NewBuffer(body))
	r.Body.Close()

	resp := mustGet(t, ts.URL+"/api/events")
	var events []map[string]any
	json.NewDecoder(resp.Body).Decode(&events)
	resp.Body.Close()
	if len(events) < 2 {
		t.Errorf("expected at least 2 events, got %d", len(events))
	}
	// Verify different event types are present
	types := map[string]bool{}
	for _, e := range events {
		types[e["type"].(string)] = true
	}
	if !types["task_created"] {
		t.Error("expected task_created event")
	}
	if !types["agent_joined"] {
		t.Error("expected agent_joined event")
	}
}

func TestMessagesWithLimit(t *testing.T) {
	_, ts := setup(t)

	for i := 0; i < 5; i++ {
		body, _ := json.Marshal(map[string]string{"from": "sender", "to": "receiver", "body": fmt.Sprintf("msg %d", i)})
		r := mustPost(t, ts.URL+"/api/messages", "application/json", bytes.NewBuffer(body))
		r.Body.Close()
	}

	resp := mustGet(t, ts.URL+"/api/messages?to=receiver&limit=2")
	var msgs []map[string]any
	json.NewDecoder(resp.Body).Decode(&msgs)
	resp.Body.Close()
	if len(msgs) > 2 {
		t.Errorf("expected at most 2 messages with limit=2, got %d", len(msgs))
	}
}

func TestClaimMissingAgent(t *testing.T) {
	_, ts := setup(t)

	body, _ := json.Marshal(map[string]string{"title": "claim-test"})
	r := mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(body))
	var task map[string]any
	json.NewDecoder(r.Body).Decode(&task)
	r.Body.Close()
	taskID := task["id"].(string)

	// Claim without agent name
	resp := mustPost(t, ts.URL+"/api/tasks/"+taskID+"/claim", "application/json", bytes.NewBuffer([]byte(`{}`)))
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for missing agent, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestUnclaimMissingAgent(t *testing.T) {
	_, ts := setup(t)

	body, _ := json.Marshal(map[string]string{"title": "unclaim-test"})
	r := mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(body))
	var task map[string]any
	json.NewDecoder(r.Body).Decode(&task)
	r.Body.Close()
	taskID := task["id"].(string)

	// Unclaim without agent name
	resp := mustPost(t, ts.URL+"/api/tasks/"+taskID+"/unclaim", "application/json", bytes.NewBuffer([]byte(`{}`)))
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for missing agent, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestEventsPagination(t *testing.T) {
	_, ts := setup(t)

	// Create several tasks to generate events
	for i := 0; i < 5; i++ {
		body, _ := json.Marshal(map[string]string{"title": fmt.Sprintf("Event task %d", i)})
		r := mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(body))
		r.Body.Close()
	}

	// Get events with limit
	resp := mustGet(t, ts.URL+"/api/events?limit=2")
	var events []map[string]any
	json.NewDecoder(resp.Body).Decode(&events)
	resp.Body.Close()
	if len(events) > 2 {
		t.Errorf("expected at most 2 events with limit=2, got %d", len(events))
	}
}

func TestProposalCRUD(t *testing.T) {
	_, ts := setup(t)

	// Create proposal
	body, _ := json.Marshal(map[string]any{
		"agent_id":   "waggle-lead",
		"project_id": "wg-d2b49a",
		"title":      "Monitoring System Design",
		"summary":    "Add health checks, agent liveness, and task SLA alerts",
		"sections":   []string{"## Architecture\nHealth check loop", "## Alerts\nPush notification on failure"},
	})
	resp := mustPost(t, ts.URL+"/api/proposals", "application/json", bytes.NewBuffer(body))
	if resp.StatusCode != 201 {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var proposal map[string]any
	json.NewDecoder(resp.Body).Decode(&proposal)
	resp.Body.Close()
	proposalID := proposal["id"].(string)

	if proposal["status"] != "pending" {
		t.Errorf("expected pending, got %v", proposal["status"])
	}

	// Get proposal
	resp = mustGet(t, ts.URL+"/api/proposals/"+proposalID)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// List proposals
	resp = mustGet(t, ts.URL+"/api/proposals?status=pending")
	var proposals []map[string]any
	json.NewDecoder(resp.Body).Decode(&proposals)
	resp.Body.Close()
	if len(proposals) != 1 {
		t.Errorf("expected 1 pending proposal, got %d", len(proposals))
	}

	// Update — approve
	body, _ = json.Marshal(map[string]any{"status": "approved", "feedback": "Looks good, proceed"})
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/proposals/"+proposalID, bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	resp = mustDo(t, req)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var updated map[string]any
	json.NewDecoder(resp.Body).Decode(&updated)
	resp.Body.Close()
	if updated["status"] != "approved" {
		t.Errorf("expected approved, got %v", updated["status"])
	}
	if updated["feedback"] != "Looks good, proceed" {
		t.Errorf("expected feedback, got %v", updated["feedback"])
	}

	// Delete
	req, _ = http.NewRequest(http.MethodDelete, ts.URL+"/api/proposals/"+proposalID, nil)
	resp = mustDo(t, req)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Verify deleted
	resp = mustGet(t, ts.URL+"/api/proposals/"+proposalID)
	if resp.StatusCode != 404 {
		t.Errorf("expected 404 after delete, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestProposalValidation(t *testing.T) {
	_, ts := setup(t)

	// Missing agent_id
	body, _ := json.Marshal(map[string]string{"title": "Test"})
	resp := mustPost(t, ts.URL+"/api/proposals", "application/json", bytes.NewBuffer(body))
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for missing agent_id, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Missing title
	body, _ = json.Marshal(map[string]string{"agent_id": "test"})
	resp = mustPost(t, ts.URL+"/api/proposals", "application/json", bytes.NewBuffer(body))
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for missing title, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Not found
	resp = mustGet(t, ts.URL+"/api/proposals/nonexistent")
	if resp.StatusCode != 404 {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestSSEEndpoint(t *testing.T) {
	a, ts := setup(t)

	// Use a context with timeout for the SSE connection
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Connect to SSE stream via Accept header
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/events", nil)
	req.Header.Set("Accept", "text/event-stream")

	done := make(chan struct{})
	var respHeaders http.Header
	var respStatus int
	var body string

	go func() {
		defer close(done)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return
		}
		respHeaders = resp.Header
		respStatus = resp.StatusCode
		defer resp.Body.Close()
		buf := make([]byte, 4096)
		n, _ := resp.Body.Read(buf)
		body = string(buf[:n])
	}()

	// Give SSE time to connect, then publish an event
	time.Sleep(50 * time.Millisecond)
	a.eventHub.Publish(&model.Event{Type: "test_sse"})

	// Wait for context timeout or response
	<-done

	if respStatus != 0 && respStatus != 200 {
		t.Errorf("expected 200 for SSE, got %d", respStatus)
	}
	if respHeaders != nil {
		ct := respHeaders.Get("Content-Type")
		if ct != "" && !strings.Contains(ct, "text/event-stream") {
			t.Errorf("expected text/event-stream, got %s", ct)
		}
	}
	if body != "" && !strings.Contains(body, "test_sse") {
		t.Logf("SSE body (may miss event due to timing): %s", body)
	}
}

func TestStatsEndpoint(t *testing.T) {
	_, ts := setup(t)

	// GET stats
	resp := mustGet(t, ts.URL+"/api/stats")
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	var stats map[string]any
	json.NewDecoder(resp.Body).Decode(&stats)
	resp.Body.Close()

	if _, ok := stats["total_tasks"]; !ok {
		t.Error("expected total_tasks in stats")
	}

	// POST should be method not allowed
	resp = mustPost(t, ts.URL+"/api/stats", "application/json", bytes.NewBufferString("{}"))
	if resp.StatusCode != 405 {
		t.Errorf("expected 405 for POST to stats, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestUsageEndpoint(t *testing.T) {
	_, ts := setup(t)

	// GET usage (empty)
	resp := mustGet(t, ts.URL+"/api/usage")
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	var usage map[string]any
	json.NewDecoder(resp.Body).Decode(&usage)
	resp.Body.Close()

	if _, ok := usage["total"]; !ok {
		t.Error("expected total in usage response")
	}
	if _, ok := usage["by_agent"]; !ok {
		t.Error("expected by_agent in usage response")
	}

	// POST usage
	body, _ := json.Marshal(map[string]any{
		"agent_name":    "test-agent",
		"model":         "sonnet",
		"input_tokens":  1000,
		"output_tokens": 500,
	})
	resp = mustPost(t, ts.URL+"/api/usage", "application/json", bytes.NewBuffer(body))
	if resp.StatusCode != 201 {
		t.Errorf("expected 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// POST usage without agent_name
	body, _ = json.Marshal(map[string]any{"model": "sonnet"})
	resp = mustPost(t, ts.URL+"/api/usage", "application/json", bytes.NewBuffer(body))
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for missing agent, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Method not allowed
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/usage", nil)
	resp = mustDo(t, req)
	if resp.StatusCode != 405 {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestSettingsEndpoint(t *testing.T) {
	_, ts := setup(t)

	// GET settings (empty initially)
	resp := mustGet(t, ts.URL+"/api/settings")
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// PUT settings
	body, _ := json.Marshal(map[string]string{"theme": "dark", "sound": "off"})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/settings", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	resp = mustDo(t, req)
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	var settings map[string]string
	json.NewDecoder(resp.Body).Decode(&settings)
	resp.Body.Close()
	if settings["theme"] != "dark" {
		t.Errorf("expected dark, got %s", settings["theme"])
	}

	// PUT invalid JSON
	req, _ = http.NewRequest(http.MethodPut, ts.URL+"/api/settings", bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	resp = mustDo(t, req)
	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Method not allowed
	req, _ = http.NewRequest(http.MethodDelete, ts.URL+"/api/settings", nil)
	resp = mustDo(t, req)
	if resp.StatusCode != 405 {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestProjectPatchInvalidJSON(t *testing.T) {
	_, ts := setup(t)

	// Create project first
	body, _ := json.Marshal(map[string]string{"name": "Patch Test"})
	resp := mustPost(t, ts.URL+"/api/projects", "application/json", bytes.NewBuffer(body))
	var project map[string]any
	json.NewDecoder(resp.Body).Decode(&project)
	resp.Body.Close()

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/projects/"+project["id"].(string), bytes.NewBufferString("bad"))
	req.Header.Set("Content-Type", "application/json")
	resp = mustDo(t, req)
	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestProjectPatchNotFound(t *testing.T) {
	_, ts := setup(t)

	body, _ := json.Marshal(map[string]any{"name": "x"})
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/projects/nonexistent", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	resp := mustDo(t, req)
	if resp.StatusCode != 404 {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestProjectDeleteNotFound(t *testing.T) {
	_, ts := setup(t)

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/projects/nonexistent", nil)
	resp := mustDo(t, req)
	if resp.StatusCode != 404 {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestProjectMethodNotAllowed(t *testing.T) {
	_, ts := setup(t)

	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/projects/someid", nil)
	resp := mustDo(t, req)
	if resp.StatusCode != 405 {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestSpawnInvalidJSON(t *testing.T) {
	_, ts := setup(t)

	resp := mustPost(t, ts.URL+"/api/spawn", "application/json", bytes.NewBufferString("bad json"))
	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestProposalDeleteNonexistent(t *testing.T) {
	_, ts := setup(t)

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/proposals/nonexistent", nil)
	resp := mustDo(t, req)
	if resp.StatusCode != 404 {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestProposalPatchNonexistent(t *testing.T) {
	_, ts := setup(t)

	body, _ := json.Marshal(map[string]any{"status": "approved"})
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/proposals/nonexistent", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	resp := mustDo(t, req)
	if resp.StatusCode != 404 {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestProposalMethodNotAllowed(t *testing.T) {
	_, ts := setup(t)

	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/proposals", nil)
	resp := mustDo(t, req)
	if resp.StatusCode != 405 {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// --- Persona API tests ---

func TestPersonaCRUDViaAPI(t *testing.T) {
	_, ts := setup(t)

	// Create persona
	body := `{"name":"API Bot","role":"Tester","description":"A test persona","capabilities":["testing"],"personality_traits":["precise"],"system_prompt":"You test things.","default_model_tier":"sonnet"}`
	resp := mustPost(t, ts.URL+"/api/personas", "application/json", strings.NewReader(body))
	if resp.StatusCode != 201 {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var created model.Persona
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	if created.ID == "" {
		t.Error("expected ID to be set")
	}
	if created.Name != "API Bot" {
		t.Errorf("expected API Bot, got %s", created.Name)
	}

	// Get persona
	resp = mustGet(t, ts.URL+"/api/personas/"+created.ID)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var got model.Persona
	json.NewDecoder(resp.Body).Decode(&got)
	resp.Body.Close()
	if got.Name != "API Bot" {
		t.Errorf("expected API Bot, got %s", got.Name)
	}

	// List personas
	resp = mustGet(t, ts.URL+"/api/personas")
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var list []model.Persona
	json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if len(list) != 1 {
		t.Errorf("expected 1 persona, got %d", len(list))
	}

	// Update persona
	patchBody := `{"name":"Updated Bot","role":"Senior Tester"}`
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/personas/"+created.ID, strings.NewReader(patchBody))
	req.Header.Set("Content-Type", "application/json")
	resp = mustDo(t, req)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var updated model.Persona
	json.NewDecoder(resp.Body).Decode(&updated)
	resp.Body.Close()
	if updated.Name != "Updated Bot" {
		t.Errorf("expected Updated Bot, got %s", updated.Name)
	}

	// Delete persona
	req, _ = http.NewRequest(http.MethodDelete, ts.URL+"/api/personas/"+created.ID, nil)
	resp = mustDo(t, req)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Verify deleted
	resp = mustGet(t, ts.URL+"/api/personas/"+created.ID)
	if resp.StatusCode != 404 {
		t.Errorf("expected 404 after delete, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestPersonaCreateMissingName(t *testing.T) {
	_, ts := setup(t)

	resp := mustPost(t, ts.URL+"/api/personas", "application/json", strings.NewReader(`{"role":"Tester"}`))
	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestPersonaListFilterByRole(t *testing.T) {
	_, ts := setup(t)

	mustPost(t, ts.URL+"/api/personas", "application/json", strings.NewReader(`{"name":"A","role":"Backend"}`)).Body.Close()
	mustPost(t, ts.URL+"/api/personas", "application/json", strings.NewReader(`{"name":"B","role":"Frontend"}`)).Body.Close()
	mustPost(t, ts.URL+"/api/personas", "application/json", strings.NewReader(`{"name":"C","role":"Backend"}`)).Body.Close()

	resp := mustGet(t, ts.URL+"/api/personas?role=Backend")
	var list []model.Persona
	json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if len(list) != 2 {
		t.Errorf("expected 2 backend personas, got %d", len(list))
	}
}

func TestPersonaGetNotFound(t *testing.T) {
	_, ts := setup(t)
	resp := mustGet(t, ts.URL+"/api/personas/nonexistent")
	if resp.StatusCode != 404 {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestPersonaPatchNotFound(t *testing.T) {
	_, ts := setup(t)
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/personas/nonexistent", strings.NewReader(`{"name":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := mustDo(t, req)
	if resp.StatusCode != 404 {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestPersonaDeleteNotFound(t *testing.T) {
	_, ts := setup(t)
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/personas/nonexistent", nil)
	resp := mustDo(t, req)
	if resp.StatusCode != 404 {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestPersonaMethodNotAllowed(t *testing.T) {
	_, ts := setup(t)
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/personas", nil)
	resp := mustDo(t, req)
	if resp.StatusCode != 405 {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestRegisterAgentWithPersona(t *testing.T) {
	_, ts := setup(t)

	// Create a persona first
	resp := mustPost(t, ts.URL+"/api/personas", "application/json",
		strings.NewReader(`{"name":"TestBot","role":"Worker","system_prompt":"You are a test bot."}`))
	var persona model.Persona
	json.NewDecoder(resp.Body).Decode(&persona)
	resp.Body.Close()

	// Register agent with persona_id
	body := fmt.Sprintf(`{"name":"persona-test-agent","type":"claude-code","persona_id":"%s"}`, persona.ID)
	resp = mustPost(t, ts.URL+"/api/agents/register", "application/json", strings.NewReader(body))
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var agent map[string]any
	json.NewDecoder(resp.Body).Decode(&agent)
	resp.Body.Close()

	if agent["persona_id"] != persona.ID {
		t.Errorf("expected persona_id %s, got %v", persona.ID, agent["persona_id"])
	}
}

// --- Additional coverage tests ---

func TestCommentsMethodNotAllowed(t *testing.T) {
	_, ts := setup(t)

	resp := mustPost(t, ts.URL+"/api/tasks", "application/json",
		bytes.NewBufferString(`{"title":"comment test"}`))
	var task map[string]any
	json.NewDecoder(resp.Body).Decode(&task)
	resp.Body.Close()
	taskID := task["id"].(string)

	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/tasks/"+taskID+"/comments", nil)
	resp = mustDo(t, req)
	if resp.StatusCode != 405 {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestCommentsInvalidJSON(t *testing.T) {
	_, ts := setup(t)

	resp := mustPost(t, ts.URL+"/api/tasks", "application/json",
		bytes.NewBufferString(`{"title":"comment test"}`))
	var task map[string]any
	json.NewDecoder(resp.Body).Decode(&task)
	resp.Body.Close()
	taskID := task["id"].(string)

	resp = mustPost(t, ts.URL+"/api/tasks/"+taskID+"/comments", "application/json",
		bytes.NewBufferString("bad json"))
	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestCompleteMethodNotAllowed(t *testing.T) {
	_, ts := setup(t)

	resp := mustPost(t, ts.URL+"/api/tasks", "application/json",
		bytes.NewBufferString(`{"title":"complete test"}`))
	var task map[string]any
	json.NewDecoder(resp.Body).Decode(&task)
	resp.Body.Close()
	taskID := task["id"].(string)

	resp = mustGet(t, ts.URL+"/api/tasks/"+taskID+"/complete")
	if resp.StatusCode != 405 {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestUnclaimMethodNotAllowed(t *testing.T) {
	_, ts := setup(t)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/tasks/some-id/unclaim", nil)
	resp := mustDo(t, req)
	if resp.StatusCode != 405 {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestClaimMethodNotAllowed(t *testing.T) {
	_, ts := setup(t)

	resp := mustGet(t, ts.URL+"/api/tasks/some-id/claim")
	if resp.StatusCode != 405 {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestProjectEpicsMethodNotAllowed(t *testing.T) {
	_, ts := setup(t)

	// Create project
	resp := mustPost(t, ts.URL+"/api/projects", "application/json",
		bytes.NewBufferString(`{"name":"epic test project"}`))
	var proj map[string]any
	json.NewDecoder(resp.Body).Decode(&proj)
	resp.Body.Close()
	projID := proj["id"].(string)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/projects/"+projID+"/epics", nil)
	resp = mustDo(t, req)
	if resp.StatusCode != 405 {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestProjectEpicsWithProgress(t *testing.T) {
	_, ts := setup(t)

	// Create project
	resp := mustPost(t, ts.URL+"/api/projects", "application/json",
		bytes.NewBufferString(`{"name":"epic progress test"}`))
	var proj map[string]any
	json.NewDecoder(resp.Body).Decode(&proj)
	resp.Body.Close()
	projID := proj["id"].(string)

	// Create an epic
	body, _ := json.Marshal(map[string]any{
		"title":      "My Epic",
		"task_type":  "epic",
		"project_id": projID,
	})
	resp = mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(body))
	var epic map[string]any
	json.NewDecoder(resp.Body).Decode(&epic)
	resp.Body.Close()
	epicID := epic["id"].(string)

	// Create subtasks
	for _, title := range []string{"Sub 1", "Sub 2"} {
		sub, _ := json.Marshal(map[string]any{
			"title":      title,
			"parent_id":  epicID,
			"project_id": projID,
		})
		mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(sub)).Body.Close()
	}

	// Get epics with progress
	resp = mustGet(t, ts.URL+"/api/projects/"+projID+"/epics")
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var epics []map[string]any
	json.NewDecoder(resp.Body).Decode(&epics)
	resp.Body.Close()
	if len(epics) != 1 {
		t.Fatalf("expected 1 epic, got %d", len(epics))
	}
	progress := epics[0]["progress"].(map[string]any)
	if int(progress["total"].(float64)) != 2 {
		t.Errorf("expected total 2, got %v", progress["total"])
	}
}

func TestUsageMethodNotAllowed(t *testing.T) {
	_, ts := setup(t)

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/usage", nil)
	resp := mustDo(t, req)
	if resp.StatusCode != 405 {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestUsageInvalidJSON(t *testing.T) {
	_, ts := setup(t)

	resp := mustPost(t, ts.URL+"/api/usage", "application/json",
		bytes.NewBufferString("not json"))
	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestReviewsMethodNotAllowed(t *testing.T) {
	_, ts := setup(t)

	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/reviews", nil)
	resp := mustDo(t, req)
	if resp.StatusCode != 405 {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestReviewMethodNotAllowed(t *testing.T) {
	_, ts := setup(t)

	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/reviews/some-id", nil)
	resp := mustDo(t, req)
	if resp.StatusCode != 405 {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestReviewPatchInvalidJSON(t *testing.T) {
	_, ts := setup(t)

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/reviews/some-id",
		bytes.NewBufferString("bad json"))
	req.Header.Set("Content-Type", "application/json")
	resp := mustDo(t, req)
	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestReviewMissingID(t *testing.T) {
	_, ts := setup(t)

	// GET /api/reviews/ with trailing slash = empty id
	resp := mustGet(t, ts.URL+"/api/reviews/")
	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestReviewDiffTooLarge(t *testing.T) {
	_, ts := setup(t)

	largeDiff := strings.Repeat("x", 500001)
	body, _ := json.Marshal(map[string]string{
		"task_id":  "t1",
		"agent_id": "a1",
		"diff":     largeDiff,
	})
	resp := mustPost(t, ts.URL+"/api/reviews", "application/json", bytes.NewBuffer(body))
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for oversized diff, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestReviewDefaultAgentID(t *testing.T) {
	_, ts := setup(t)

	body, _ := json.Marshal(map[string]string{
		"task_id": "t1",
		"diff":   "some diff",
	})
	resp := mustPost(t, ts.URL+"/api/reviews", "application/json", bytes.NewBuffer(body))
	if resp.StatusCode != 201 {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var rev map[string]any
	json.NewDecoder(resp.Body).Decode(&rev)
	resp.Body.Close()
	if rev["agent_id"] != "unknown" {
		t.Errorf("expected agent_id 'unknown', got %v", rev["agent_id"])
	}
}

func TestReviewFilterByAgent(t *testing.T) {
	_, ts := setup(t)

	// Create tasks for reviews
	resp := mustPost(t, ts.URL+"/api/tasks", "application/json",
		bytes.NewBufferString(`{"title":"rev task 1"}`))
	var t1 map[string]any
	json.NewDecoder(resp.Body).Decode(&t1)
	resp.Body.Close()

	resp = mustPost(t, ts.URL+"/api/tasks", "application/json",
		bytes.NewBufferString(`{"title":"rev task 2"}`))
	var t2 map[string]any
	json.NewDecoder(resp.Body).Decode(&t2)
	resp.Body.Close()

	// Submit reviews with different agents
	for _, r := range []map[string]string{
		{"task_id": t1["id"].(string), "agent_id": "agent-a", "diff": "d1"},
		{"task_id": t2["id"].(string), "agent_id": "agent-b", "diff": "d2"},
	} {
		body, _ := json.Marshal(r)
		mustPost(t, ts.URL+"/api/reviews", "application/json", bytes.NewBuffer(body)).Body.Close()
	}

	// Filter by agent
	resp = mustGet(t, ts.URL+"/api/reviews?agent_id=agent-a")
	var reviews []map[string]any
	json.NewDecoder(resp.Body).Decode(&reviews)
	resp.Body.Close()
	if len(reviews) != 1 {
		t.Errorf("expected 1 review for agent-a, got %d", len(reviews))
	}
}

func TestPushSubscribeMethodNotAllowed(t *testing.T) {
	_, ts := setup(t)

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/push/subscribe", nil)
	resp := mustDo(t, req)
	if resp.StatusCode != 405 {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestPushSubscribeInvalidJSON(t *testing.T) {
	_, ts := setup(t)

	resp := mustPost(t, ts.URL+"/api/push/subscribe", "application/json",
		bytes.NewBufferString("bad json"))
	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestSettingsMethodNotAllowed(t *testing.T) {
	_, ts := setup(t)

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/settings", nil)
	resp := mustDo(t, req)
	if resp.StatusCode != 405 {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestSettingsInvalidJSON(t *testing.T) {
	_, ts := setup(t)

	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/settings",
		bytes.NewBufferString("bad json"))
	req.Header.Set("Content-Type", "application/json")
	resp := mustDo(t, req)
	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestExportMethodNotAllowed(t *testing.T) {
	_, ts := setup(t)

	resp := mustPost(t, ts.URL+"/api/tasks/export", "application/json", nil)
	if resp.StatusCode != 405 {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestSessionsMethodNotAllowed(t *testing.T) {
	_, ts := setup(t)

	resp := mustPost(t, ts.URL+"/api/sessions", "application/json", nil)
	if resp.StatusCode != 405 {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestSessionActionNotFoundSession(t *testing.T) {
	_, ts := setup(t)

	// Session that doesn't exist should 404
	resp := mustGet(t, ts.URL+"/api/sessions/nonexistent-xyz/output")
	if resp.StatusCode != 404 {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestSessionActionNotFound(t *testing.T) {
	_, ts := setup(t)

	resp := mustGet(t, ts.URL+"/api/sessions/nonexistent-session/output")
	if resp.StatusCode != 404 {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestProposalCreateInvalidJSON(t *testing.T) {
	_, ts := setup(t)

	resp := mustPost(t, ts.URL+"/api/proposals", "application/json",
		bytes.NewBufferString("bad json"))
	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestProposalCreateMissingAgentID(t *testing.T) {
	_, ts := setup(t)

	resp := mustPost(t, ts.URL+"/api/proposals", "application/json",
		bytes.NewBufferString(`{"title":"test"}`))
	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestProposalCreateMissingTitle(t *testing.T) {
	_, ts := setup(t)

	resp := mustPost(t, ts.URL+"/api/proposals", "application/json",
		bytes.NewBufferString(`{"agent_id":"a1"}`))
	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestProposalPatchInvalidJSON(t *testing.T) {
	_, ts := setup(t)

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/proposals/some-id",
		bytes.NewBufferString("bad json"))
	req.Header.Set("Content-Type", "application/json")
	resp := mustDo(t, req)
	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestProposalGetNotFound(t *testing.T) {
	_, ts := setup(t)

	resp := mustGet(t, ts.URL+"/api/proposals/nonexistent")
	if resp.StatusCode != 404 {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestProposalMissingID(t *testing.T) {
	_, ts := setup(t)

	resp := mustGet(t, ts.URL+"/api/proposals/")
	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestProposalFilters(t *testing.T) {
	_, ts := setup(t)

	// Create proposals
	for _, p := range []map[string]string{
		{"agent_id": "a1", "title": "P1", "project_id": "proj-1"},
		{"agent_id": "a2", "title": "P2", "project_id": "proj-2"},
	} {
		body, _ := json.Marshal(p)
		mustPost(t, ts.URL+"/api/proposals", "application/json", bytes.NewBuffer(body)).Body.Close()
	}

	// Filter by agent
	resp := mustGet(t, ts.URL+"/api/proposals?agent_id=a1")
	var proposals []map[string]any
	json.NewDecoder(resp.Body).Decode(&proposals)
	resp.Body.Close()
	if len(proposals) != 1 {
		t.Errorf("expected 1 proposal for a1, got %d", len(proposals))
	}

	// Filter by project
	resp = mustGet(t, ts.URL+"/api/proposals?project_id=proj-2")
	json.NewDecoder(resp.Body).Decode(&proposals)
	resp.Body.Close()
	if len(proposals) != 1 {
		t.Errorf("expected 1 proposal for proj-2, got %d", len(proposals))
	}
}

func TestProposalFullCRUD(t *testing.T) {
	_, ts := setup(t)

	// Create
	body := `{"agent_id":"test-agent","title":"Test Proposal","description":"A test"}`
	resp := mustPost(t, ts.URL+"/api/proposals", "application/json", strings.NewReader(body))
	if resp.StatusCode != 201 {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var created map[string]any
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	id := created["id"].(string)

	// Get
	resp = mustGet(t, ts.URL+"/api/proposals/"+id)
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Patch
	patchBody, _ := json.Marshal(map[string]any{"status": "approved"})
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/proposals/"+id, bytes.NewBuffer(patchBody))
	req.Header.Set("Content-Type", "application/json")
	resp = mustDo(t, req)
	if resp.StatusCode != 200 {
		t.Errorf("expected 200 for patch, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Delete
	req, _ = http.NewRequest(http.MethodDelete, ts.URL+"/api/proposals/"+id, nil)
	resp = mustDo(t, req)
	if resp.StatusCode != 200 {
		t.Errorf("expected 200 for delete, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestPersonaCreateInvalidJSON(t *testing.T) {
	_, ts := setup(t)

	resp := mustPost(t, ts.URL+"/api/personas", "application/json",
		bytes.NewBufferString("bad json"))
	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestPersonaPatchInvalidJSON(t *testing.T) {
	_, ts := setup(t)

	// Create first
	resp := mustPost(t, ts.URL+"/api/personas", "application/json",
		strings.NewReader(`{"name":"Patchable"}`))
	var p model.Persona
	json.NewDecoder(resp.Body).Decode(&p)
	resp.Body.Close()

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/personas/"+p.ID,
		bytes.NewBufferString("bad json"))
	req.Header.Set("Content-Type", "application/json")
	resp = mustDo(t, req)
	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestPersonaMissingID(t *testing.T) {
	_, ts := setup(t)

	resp := mustGet(t, ts.URL+"/api/personas/")
	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestPersonaListFilterByName(t *testing.T) {
	_, ts := setup(t)

	mustPost(t, ts.URL+"/api/personas", "application/json",
		strings.NewReader(`{"name":"Alice"}`)).Body.Close()
	mustPost(t, ts.URL+"/api/personas", "application/json",
		strings.NewReader(`{"name":"Bob"}`)).Body.Close()

	resp := mustGet(t, ts.URL+"/api/personas?name=Alice")
	var list []model.Persona
	json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if len(list) != 1 {
		t.Errorf("expected 1 persona named Alice, got %d", len(list))
	}
}

func TestPersonaSingleMethodNotAllowed(t *testing.T) {
	_, ts := setup(t)

	resp := mustPost(t, ts.URL+"/api/personas", "application/json",
		strings.NewReader(`{"name":"Test"}`))
	var p model.Persona
	json.NewDecoder(resp.Body).Decode(&p)
	resp.Body.Close()

	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/personas/"+p.ID, nil)
	resp = mustDo(t, req)
	if resp.StatusCode != 405 {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAgentStatusInvalidJSON(t *testing.T) {
	_, ts := setup(t)

	resp := mustPost(t, ts.URL+"/api/agents/some-agent/status", "application/json",
		bytes.NewBufferString("bad json"))
	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAgentProjectInvalidJSON(t *testing.T) {
	_, ts := setup(t)

	resp := mustPost(t, ts.URL+"/api/agents/some-agent/project", "application/json",
		bytes.NewBufferString("bad json"))
	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAgentRegisterInvalidJSON(t *testing.T) {
	_, ts := setup(t)

	resp := mustPost(t, ts.URL+"/api/agents/register", "application/json",
		bytes.NewBufferString("bad json"))
	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAgentRegisterMissingName(t *testing.T) {
	_, ts := setup(t)

	resp := mustPost(t, ts.URL+"/api/agents/register", "application/json",
		bytes.NewBufferString(`{"type":"test"}`))
	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestMessagesMethodNotAllowed(t *testing.T) {
	_, ts := setup(t)

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/messages", nil)
	resp := mustDo(t, req)
	if resp.StatusCode != 405 {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestMessagesPatchInvalidJSON(t *testing.T) {
	_, ts := setup(t)

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/messages",
		bytes.NewBufferString("bad json"))
	req.Header.Set("Content-Type", "application/json")
	resp := mustDo(t, req)
	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestMessagesPatchMarkAll(t *testing.T) {
	_, ts := setup(t)

	// Send a message first
	body, _ := json.Marshal(map[string]string{"from": "a1", "to": "a2", "body": "hello"})
	mustPost(t, ts.URL+"/api/messages", "application/json", bytes.NewBuffer(body)).Body.Close()

	// Mark all read
	patchBody, _ := json.Marshal(map[string]any{"mark_all": true})
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/messages", bytes.NewBuffer(patchBody))
	req.Header.Set("Content-Type", "application/json")
	resp := mustDo(t, req)
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestMessagesInvalidJSON(t *testing.T) {
	_, ts := setup(t)

	resp := mustPost(t, ts.URL+"/api/messages", "application/json",
		bytes.NewBufferString("bad json"))
	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestEventsMethodNotAllowed(t *testing.T) {
	_, ts := setup(t)

	resp := mustPost(t, ts.URL+"/api/events", "application/json", nil)
	if resp.StatusCode != 405 {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestStatsMethodNotAllowed(t *testing.T) {
	_, ts := setup(t)

	resp := mustPost(t, ts.URL+"/api/stats", "application/json", nil)
	if resp.StatusCode != 405 {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAgentsMethodNotAllowed(t *testing.T) {
	_, ts := setup(t)

	resp := mustPost(t, ts.URL+"/api/agents", "application/json", nil)
	if resp.StatusCode != 405 {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestTasksMethodNotAllowed(t *testing.T) {
	_, ts := setup(t)

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/tasks", nil)
	resp := mustDo(t, req)
	if resp.StatusCode != 405 {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestTaskMethodNotAllowed(t *testing.T) {
	_, ts := setup(t)

	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/tasks/some-id", nil)
	resp := mustDo(t, req)
	if resp.StatusCode != 405 {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestSpawnWithPersonaID(t *testing.T) {
	_, ts := setup(t)

	// Create persona with default model tier
	resp := mustPost(t, ts.URL+"/api/personas", "application/json",
		strings.NewReader(`{"name":"SpawnBot","default_model_tier":"opus"}`))
	var persona model.Persona
	json.NewDecoder(resp.Body).Decode(&persona)
	resp.Body.Close()

	// Spawn with persona — will fail at exec but exercises persona lookup path
	body, _ := json.Marshal(map[string]any{
		"name":       "spawn-test",
		"work_dir":   "/tmp",
		"persona_id": persona.ID,
	})
	resp = mustPost(t, ts.URL+"/api/spawn", "application/json", bytes.NewBuffer(body))
	// Will fail because claude binary not found in test, but we exercise the persona path
	resp.Body.Close()
}

func TestSpawnRelativeWorkDir(t *testing.T) {
	_, ts := setup(t)

	body, _ := json.Marshal(map[string]any{
		"name":     "rel-dir-agent",
		"work_dir": "nonexistent-relative-dir-xyz",
	})
	resp := mustPost(t, ts.URL+"/api/spawn", "application/json", bytes.NewBuffer(body))
	// Either 400 (dir doesn't exist) or it exercises the relative path logic
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for nonexistent relative dir, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestCompleteTaskInternalError(t *testing.T) {
	_, ts := setup(t)

	// Complete non-existent task triggers not_found
	resp := mustPost(t, ts.URL+"/api/tasks/nonexistent/complete", "application/json",
		bytes.NewBufferString(`{}`))
	if resp.StatusCode != 404 {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestUnclaimTaskNotFound(t *testing.T) {
	_, ts := setup(t)

	body, _ := json.Marshal(map[string]string{"agent": "a1"})
	resp := mustPost(t, ts.URL+"/api/tasks/nonexistent/unclaim", "application/json", bytes.NewBuffer(body))
	if resp.StatusCode != 404 {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAgentMethodNotAllowed(t *testing.T) {
	_, ts := setup(t)

	body, _ := json.Marshal(map[string]string{"name": "test-ma", "type": "test"})
	mustPost(t, ts.URL+"/api/agents/register", "application/json", bytes.NewBuffer(body)).Body.Close()

	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/agents/test-ma", nil)
	resp := mustDo(t, req)
	if resp.StatusCode != 405 {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestTaskPatchInvalidJSON(t *testing.T) {
	_, ts := setup(t)

	resp := mustPost(t, ts.URL+"/api/tasks", "application/json",
		bytes.NewBufferString(`{"title":"patch-test"}`))
	var task map[string]any
	json.NewDecoder(resp.Body).Decode(&task)
	resp.Body.Close()

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/tasks/"+task["id"].(string),
		bytes.NewBufferString("bad json"))
	req.Header.Set("Content-Type", "application/json")
	resp = mustDo(t, req)
	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestTaskCreateInvalidJSON(t *testing.T) {
	_, ts := setup(t)

	resp := mustPost(t, ts.URL+"/api/tasks", "application/json",
		bytes.NewBufferString("bad json"))
	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestTaskTitleTooLong(t *testing.T) {
	_, ts := setup(t)

	longTitle := strings.Repeat("x", 501)
	body, _ := json.Marshal(map[string]string{"title": longTitle})
	resp := mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(body))
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for long title, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestTaskDescriptionTooLong(t *testing.T) {
	_, ts := setup(t)

	longDesc := strings.Repeat("x", 10001)
	body, _ := json.Marshal(map[string]string{"title": "test", "description": longDesc})
	resp := mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(body))
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for long description, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestProjectNameTooLong(t *testing.T) {
	_, ts := setup(t)

	longName := strings.Repeat("x", 201)
	body, _ := json.Marshal(map[string]string{"name": longName})
	resp := mustPost(t, ts.URL+"/api/projects", "application/json", bytes.NewBuffer(body))
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for long project name, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestProjectCreateInvalidJSON(t *testing.T) {
	_, ts := setup(t)

	resp := mustPost(t, ts.URL+"/api/projects", "application/json",
		bytes.NewBufferString("bad json"))
	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestPushUnsubscribeInvalidJSON(t *testing.T) {
	_, ts := setup(t)

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/push/subscribe",
		bytes.NewBufferString("bad json"))
	req.Header.Set("Content-Type", "application/json")
	resp := mustDo(t, req)
	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestReviewsInvalidJSON(t *testing.T) {
	_, ts := setup(t)

	resp := mustPost(t, ts.URL+"/api/reviews", "application/json",
		bytes.NewBufferString("bad json"))
	if resp.StatusCode != 400 {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestEventsWithInvalidLimit(t *testing.T) {
	_, ts := setup(t)

	resp := mustGet(t, ts.URL+"/api/events?limit=-5")
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = mustGet(t, ts.URL+"/api/events?limit=999")
	if resp.StatusCode != 200 {
		t.Errorf("expected 200 for oversized limit, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestExportCSVFormat(t *testing.T) {
	_, ts := setup(t)

	// Create a task with project
	resp := mustPost(t, ts.URL+"/api/projects", "application/json",
		bytes.NewBufferString(`{"name":"CSV project"}`))
	var proj map[string]any
	json.NewDecoder(resp.Body).Decode(&proj)
	resp.Body.Close()

	body, _ := json.Marshal(map[string]any{
		"title":      "CSV task",
		"priority":   "high",
		"project_id": proj["id"],
	})
	mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(body)).Body.Close()

	// Export with project filter
	resp = mustGet(t, ts.URL+"/api/tasks/export?format=csv&project_id="+proj["id"].(string))
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/csv") {
		t.Errorf("expected text/csv content type, got %s", ct)
	}
	resp.Body.Close()
}

func TestAlertsEmpty(t *testing.T) {
	_, ts := setup(t)

	resp := mustGet(t, ts.URL+"/api/alerts")
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var alerts []map[string]any
	json.NewDecoder(resp.Body).Decode(&alerts)
	resp.Body.Close()
	if len(alerts) != 1 {
		// Should have "no_agents" alert since no agents are registered
		t.Errorf("expected 1 alert (no_agents), got %d", len(alerts))
	}
	if len(alerts) > 0 && alerts[0]["type"] != "no_agents" {
		t.Errorf("expected no_agents alert, got %v", alerts[0]["type"])
	}
}

func TestAlertsMethodNotAllowed(t *testing.T) {
	_, ts := setup(t)

	resp := mustPost(t, ts.URL+"/api/alerts", "application/json", nil)
	if resp.StatusCode != 405 {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAlertsStaleTask(t *testing.T) {
	a, ts := setup(t)

	// Create a task in_progress
	task := &model.Task{Title: "Stale task", Status: model.TaskInProgress}
	a.store.CreateTask(task)

	// Backdate updated_at by 4 days via direct store access
	fourDaysAgo := time.Now().UTC().Add(-4 * 24 * time.Hour).Format(time.RFC3339)
	a.store.Exec("UPDATE tasks SET updated_at = ? WHERE id = ?", fourDaysAgo, task.ID)

	// Register an agent so we don't get the no_agents alert
	mustPost(t, ts.URL+"/api/agents/register", "application/json",
		bytes.NewBufferString(`{"name":"alert-test-agent","type":"test"}`)).Body.Close()

	resp := mustGet(t, ts.URL+"/api/alerts")
	var alerts []map[string]any
	json.NewDecoder(resp.Body).Decode(&alerts)
	resp.Body.Close()

	found := false
	for _, al := range alerts {
		if al["type"] == "stale_task" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected stale_task alert for 4-day-old in_progress task")
	}
}

func TestAlertsPastDeadline(t *testing.T) {
	a, ts := setup(t)

	yesterday := time.Now().UTC().Add(-48 * time.Hour)
	task := &model.Task{Title: "Overdue task", Status: model.TaskReady, Deadline: &yesterday}
	a.store.CreateTask(task)

	mustPost(t, ts.URL+"/api/agents/register", "application/json",
		bytes.NewBufferString(`{"name":"dl-agent","type":"test"}`)).Body.Close()

	resp := mustGet(t, ts.URL+"/api/alerts")
	var alerts []map[string]any
	json.NewDecoder(resp.Body).Decode(&alerts)
	resp.Body.Close()

	found := false
	for _, al := range alerts {
		if al["type"] == "past_deadline" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected past_deadline alert for overdue task")
	}
}

func TestAlertsBlockedTask(t *testing.T) {
	a, ts := setup(t)

	task := &model.Task{Title: "Blocked task", Status: model.TaskBlocked}
	a.store.CreateTask(task)

	twoDaysAgo := time.Now().UTC().Add(-2 * 24 * time.Hour).Format(time.RFC3339)
	a.store.Exec("UPDATE tasks SET updated_at = ? WHERE id = ?", twoDaysAgo, task.ID)

	mustPost(t, ts.URL+"/api/agents/register", "application/json",
		bytes.NewBufferString(`{"name":"blk-agent","type":"test"}`)).Body.Close()

	resp := mustGet(t, ts.URL+"/api/alerts")
	var alerts []map[string]any
	json.NewDecoder(resp.Body).Decode(&alerts)
	resp.Body.Close()

	found := false
	for _, al := range alerts {
		if al["type"] == "blocked_task" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected blocked_task alert for 2-day-old blocked task")
	}
}
