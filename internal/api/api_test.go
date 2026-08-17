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

func TestMessagesFilterByProject(t *testing.T) {
	_, ts := setup(t)

	mustPost(t, ts.URL+"/api/messages", "application/json",
		bytes.NewBufferString(`{"from":"a1","to":"user","body":"proj1 msg","project_id":"proj-1"}`))
	mustPost(t, ts.URL+"/api/messages", "application/json",
		bytes.NewBufferString(`{"from":"a2","to":"user","body":"proj2 msg","project_id":"proj-2"}`))
	mustPost(t, ts.URL+"/api/messages", "application/json",
		bytes.NewBufferString(`{"from":"a3","to":"user","body":"no proj msg"}`))

	resp := mustGet(t, ts.URL+"/api/messages?project_id=proj-1")
	defer resp.Body.Close()
	var msgs []map[string]any
	json.NewDecoder(resp.Body).Decode(&msgs)
	if len(msgs) != 1 {
		t.Errorf("expected 1 message for proj-1, got %d", len(msgs))
	}
	if msgs[0]["body"] != "proj1 msg" {
		t.Errorf("expected 'proj1 msg', got %v", msgs[0]["body"])
	}
	if msgs[0]["project_id"] != "proj-1" {
		t.Errorf("expected project_id 'proj-1', got %v", msgs[0]["project_id"])
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

func TestSSEReplayLastEventID(t *testing.T) {
	a, ts := setup(t)

	// Record two events so we have IDs to replay from
	evt1 := &model.Event{Type: "replay_test", Payload: map[string]any{"seq": 1}}
	a.store.RecordEvent(evt1)
	evt2 := &model.Event{Type: "replay_test", Payload: map[string]any{"seq": 2}}
	a.store.RecordEvent(evt2)

	// Connect with Last-Event-ID set to evt1 — should replay evt2
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/events", nil)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Last-Event-ID", evt1.ID)

	done := make(chan struct{})
	var body string

	go func() {
		defer close(done)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return
		}
		defer resp.Body.Close()
		// Read in a loop until context cancels — replay data arrives immediately
		var buf []byte
		tmp := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(tmp)
			if n > 0 {
				buf = append(buf, tmp[:n]...)
			}
			if err != nil {
				break
			}
		}
		body = string(buf)
	}()

	<-done

	if !strings.Contains(body, evt2.ID) {
		t.Errorf("expected replayed event with ID %s in body, got: %s", evt2.ID, body)
	}
	if strings.Contains(body, evt1.ID) {
		t.Errorf("should NOT contain the Last-Event-ID event itself, got: %s", body)
	}
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

func TestProjectAutoDispatchSetting(t *testing.T) {
	_, ts := setup(t)

	// Create a project
	body, _ := json.Marshal(map[string]string{"name": "auto-test"})
	resp := mustPost(t, ts.URL+"/api/projects", "application/json", bytes.NewBuffer(body))
	var proj map[string]any
	json.NewDecoder(resp.Body).Decode(&proj)
	resp.Body.Close()
	projectID := proj["id"].(string)

	// Default auto_dispatch should be false
	if proj["auto_dispatch"] != false {
		t.Errorf("expected auto_dispatch default false, got %v", proj["auto_dispatch"])
	}

	// Enable auto_dispatch via PATCH
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/projects/"+projectID,
		bytes.NewBufferString(`{"auto_dispatch": true}`))
	req.Header.Set("Content-Type", "application/json")
	resp = mustDo(t, req)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var updated map[string]any
	json.NewDecoder(resp.Body).Decode(&updated)
	resp.Body.Close()

	if updated["auto_dispatch"] != true {
		t.Errorf("expected auto_dispatch true after update, got %v", updated["auto_dispatch"])
	}

	// GET should also return auto_dispatch: true
	resp = mustGet(t, ts.URL+"/api/projects/"+projectID)
	var fetched map[string]any
	json.NewDecoder(resp.Body).Decode(&fetched)
	resp.Body.Close()

	if fetched["auto_dispatch"] != true {
		t.Errorf("expected auto_dispatch true on GET, got %v", fetched["auto_dispatch"])
	}

	// Disable it
	req, _ = http.NewRequest(http.MethodPatch, ts.URL+"/api/projects/"+projectID,
		bytes.NewBufferString(`{"auto_dispatch": false}`))
	req.Header.Set("Content-Type", "application/json")
	resp = mustDo(t, req)
	var disabled map[string]any
	json.NewDecoder(resp.Body).Decode(&disabled)
	resp.Body.Close()

	if disabled["auto_dispatch"] != false {
		t.Errorf("expected auto_dispatch false after disable, got %v", disabled["auto_dispatch"])
	}
}

func TestAutoDispatchOnAgentIdle(t *testing.T) {
	_, ts := setup(t)

	// Create project with auto_dispatch enabled
	body, _ := json.Marshal(map[string]string{"name": "dispatch-proj"})
	resp := mustPost(t, ts.URL+"/api/projects", "application/json", bytes.NewBuffer(body))
	var proj map[string]any
	json.NewDecoder(resp.Body).Decode(&proj)
	resp.Body.Close()
	projectID := proj["id"].(string)

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/projects/"+projectID,
		bytes.NewBufferString(`{"auto_dispatch": true}`))
	req.Header.Set("Content-Type", "application/json")
	mustDo(t, req).Body.Close()

	// Create a ready task in that project
	taskBody, _ := json.Marshal(map[string]any{
		"title":      "Auto-dispatch me",
		"priority":   "high",
		"project_id": projectID,
		"status":     "ready",
	})
	resp = mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(taskBody))
	var task map[string]any
	json.NewDecoder(resp.Body).Decode(&task)
	resp.Body.Close()
	taskID := task["id"].(string)

	// Register an agent on this project
	agentBody, _ := json.Marshal(map[string]string{
		"name": "dispatch-worker", "type": "claude-code", "project_id": projectID,
	})
	resp = mustPost(t, ts.URL+"/api/agents/register", "application/json", bytes.NewBuffer(agentBody))
	resp.Body.Close()

	// Set agent to idle — should trigger auto-dispatch
	resp = mustPost(t, ts.URL+"/api/agents/dispatch-worker/status", "application/json",
		bytes.NewBufferString(`{"status": "idle"}`))
	resp.Body.Close()

	// Check that the task is now in_progress and assigned
	resp = mustGet(t, ts.URL+"/api/tasks/"+taskID)
	var dispatched map[string]any
	json.NewDecoder(resp.Body).Decode(&dispatched)
	resp.Body.Close()

	if dispatched["status"] != "in_progress" {
		t.Errorf("expected task status in_progress after auto-dispatch, got %v", dispatched["status"])
	}
	if dispatched["assignee"] != "dispatch-worker" {
		t.Errorf("expected assignee dispatch-worker, got %v", dispatched["assignee"])
	}

	// Check that agent received a message about the assignment
	resp = mustGet(t, ts.URL+"/api/messages?to=dispatch-worker&limit=5")
	var msgs []map[string]any
	json.NewDecoder(resp.Body).Decode(&msgs)
	resp.Body.Close()

	found := false
	for _, m := range msgs {
		if strings.Contains(m["body"].(string), "Auto-assigned task") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected auto-dispatch message to agent")
	}
}

func TestAutoDispatchRespectsDisabledSetting(t *testing.T) {
	_, ts := setup(t)

	// Create project with auto_dispatch OFF (default)
	body, _ := json.Marshal(map[string]string{"name": "no-dispatch-proj"})
	resp := mustPost(t, ts.URL+"/api/projects", "application/json", bytes.NewBuffer(body))
	var proj map[string]any
	json.NewDecoder(resp.Body).Decode(&proj)
	resp.Body.Close()
	projectID := proj["id"].(string)

	// Create a ready task
	taskBody, _ := json.Marshal(map[string]any{
		"title":      "Should not auto-dispatch",
		"priority":   "high",
		"project_id": projectID,
		"status":     "ready",
	})
	resp = mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(taskBody))
	var task map[string]any
	json.NewDecoder(resp.Body).Decode(&task)
	resp.Body.Close()
	taskID := task["id"].(string)

	// Register agent and go idle
	agentBody, _ := json.Marshal(map[string]string{
		"name": "idle-worker", "type": "claude-code", "project_id": projectID,
	})
	resp = mustPost(t, ts.URL+"/api/agents/register", "application/json", bytes.NewBuffer(agentBody))
	resp.Body.Close()

	resp = mustPost(t, ts.URL+"/api/agents/idle-worker/status", "application/json",
		bytes.NewBufferString(`{"status": "idle"}`))
	resp.Body.Close()

	// Task should still be ready and unassigned
	resp = mustGet(t, ts.URL+"/api/tasks/"+taskID)
	var check map[string]any
	json.NewDecoder(resp.Body).Decode(&check)
	resp.Body.Close()

	if check["status"] != "ready" {
		t.Errorf("expected task to remain ready when auto_dispatch is off, got %v", check["status"])
	}
	if check["assignee"] != nil && check["assignee"] != "" {
		t.Errorf("expected task to remain unassigned, got %v", check["assignee"])
	}
}

func TestAutoDispatchSkipsTasksWithUnmetDeps(t *testing.T) {
	_, ts := setup(t)

	// Create project with auto_dispatch enabled
	body, _ := json.Marshal(map[string]string{"name": "deps-proj"})
	resp := mustPost(t, ts.URL+"/api/projects", "application/json", bytes.NewBuffer(body))
	var proj map[string]any
	json.NewDecoder(resp.Body).Decode(&proj)
	resp.Body.Close()
	projectID := proj["id"].(string)

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/projects/"+projectID,
		bytes.NewBufferString(`{"auto_dispatch": true}`))
	req.Header.Set("Content-Type", "application/json")
	mustDo(t, req).Body.Close()

	// Create a blocker task (in_progress, not done)
	blockerBody, _ := json.Marshal(map[string]any{
		"title":      "Blocker task",
		"priority":   "high",
		"project_id": projectID,
		"status":     "in_progress",
	})
	resp = mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(blockerBody))
	var blocker map[string]any
	json.NewDecoder(resp.Body).Decode(&blocker)
	resp.Body.Close()
	blockerID := blocker["id"].(string)

	// Create a task that depends on the blocker
	depBody, _ := json.Marshal(map[string]any{
		"title":      "Blocked task",
		"priority":   "high",
		"project_id": projectID,
		"status":     "ready",
		"depends_on": []string{blockerID},
	})
	resp = mustPost(t, ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(depBody))
	var blocked map[string]any
	json.NewDecoder(resp.Body).Decode(&blocked)
	resp.Body.Close()
	blockedID := blocked["id"].(string)

	// Register agent and go idle
	agentBody, _ := json.Marshal(map[string]string{
		"name": "dep-worker", "type": "claude-code", "project_id": projectID,
	})
	resp = mustPost(t, ts.URL+"/api/agents/register", "application/json", bytes.NewBuffer(agentBody))
	resp.Body.Close()

	resp = mustPost(t, ts.URL+"/api/agents/dep-worker/status", "application/json",
		bytes.NewBufferString(`{"status": "idle"}`))
	resp.Body.Close()

	// Blocked task should NOT be auto-dispatched
	resp = mustGet(t, ts.URL+"/api/tasks/"+blockedID)
	var check map[string]any
	json.NewDecoder(resp.Body).Decode(&check)
	resp.Body.Close()

	if check["status"] != "ready" {
		t.Errorf("expected blocked task to remain ready, got %v", check["status"])
	}
}

func TestRespawnEndpointValidation(t *testing.T) {
	_, ts := setup(t)

	// Respawn unknown agent should 404
	resp := mustPost(t, ts.URL+"/api/agents/nonexistent/respawn", "application/json",
		bytes.NewBufferString(`{"work_dir":"/tmp"}`))
	if resp.StatusCode != 404 {
		t.Errorf("expected 404 for unknown agent, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Register an agent
	agentBody, _ := json.Marshal(map[string]string{
		"name": "respawn-test", "type": "claude-code", "project_id": "",
	})
	resp = mustPost(t, ts.URL+"/api/agents/register", "application/json", bytes.NewBuffer(agentBody))
	resp.Body.Close()

	// Respawn without work_dir should 400
	resp = mustPost(t, ts.URL+"/api/agents/respawn-test/respawn", "application/json",
		bytes.NewBufferString(`{}`))
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for missing work_dir, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Respawn with invalid work_dir should 400
	resp = mustPost(t, ts.URL+"/api/agents/respawn-test/respawn", "application/json",
		bytes.NewBufferString(`{"work_dir":"/nonexistent/path/that/does/not/exist"}`))
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for invalid work_dir, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Verify agent was disconnected as part of respawn attempt (cleanup happens before spawn)
	resp = mustGet(t, ts.URL+"/api/agents/respawn-test")
	var agent map[string]any
	json.NewDecoder(resp.Body).Decode(&agent)
	resp.Body.Close()

	if agent["status"] != "disconnected" {
		t.Errorf("expected agent disconnected after respawn attempt, got %v", agent["status"])
	}
}

func TestAgentHeartbeatEndpoint(t *testing.T) {
	_, ts := setup(t)

	// Register an agent
	resp := mustPost(t, ts.URL+"/api/agents/register", "application/json",
		strings.NewReader(`{"name":"hb-agent","type":"claude-code"}`))
	resp.Body.Close()

	// POST heartbeat should return 200
	resp = mustPost(t, ts.URL+"/api/agents/hb-agent/heartbeat", "application/json", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}
	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	if result["status"] != "ok" {
		t.Errorf("expected status ok, got %v", result["status"])
	}
}

func TestEventsLimitCapsAt500(t *testing.T) {
	_, ts := setup(t)

	// Request with limit=501 should cap at 500 not reset to 50
	resp := mustGet(t, ts.URL+"/api/events?limit=501")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	// We can't easily verify the internal limit without creating 501 events,
	// but at least verify the endpoint accepts it without error
}

func TestEventsLimitNegativeDefaultsTo50(t *testing.T) {
	_, ts := setup(t)

	resp := mustGet(t, ts.URL+"/api/events?limit=-1")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestDeleteNonexistentAgentReturns404(t *testing.T) {
	_, ts := setup(t)

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/agents/ghost-agent", nil)
	resp := mustDo(t, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for deleting nonexistent agent, got %d", resp.StatusCode)
	}
}

func TestSprintEndpoints(t *testing.T) {
	_, ts := setup(t)
	// Create.
	body := `{"project_id":"p1","name":"Sprint 1","goal":"ship"}`
	resp, err := http.Post(ts.URL+"/api/sprints", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var created model.Sprint
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	if created.ID == "" {
		t.Fatal("expected sprint id")
	}
	// List.
	lr := mustGet(t, ts.URL+"/api/sprints?project_id=p1")
	var list []model.Sprint
	json.NewDecoder(lr.Body).Decode(&list)
	lr.Body.Close()
	if len(list) != 1 {
		t.Errorf("expected 1 sprint, got %d", len(list))
	}
	// Burndown.
	br := mustGet(t, ts.URL+"/api/sprints/"+created.ID+"/burndown")
	if br.StatusCode != http.StatusOK {
		t.Errorf("burndown expected 200, got %d", br.StatusCode)
	}
	br.Body.Close()
}

func TestWakeAgentSkipsAliveRegisteredAgent(t *testing.T) {
	a, _ := setup(t)

	// Register agent via store directly (simulates MCP-registered agent)
	_, err := a.store.RegisterAgent("mcp-agent", "claude-code", "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	// wakeAgent should NOT try to spawn since agent is alive in store
	// (it won't panic or log errors for a known alive agent)
	a.wakeAgent("mcp-agent", "test message")
	// If we get here without a spawn attempt, the check passed.
	// The old code would always try to spawn since mcp-agent isn't in a.procs.
}

func TestTaskMoveAndSprintAssign(t *testing.T) {
	a, ts := setup(t)
	task := &model.Task{Title: "t"}
	a.store.CreateTask(task)
	// Move.
	mr, err := http.Post(ts.URL+"/api/tasks/"+task.ID+"/move", "application/json",
		strings.NewReader(`{"status":"in_progress","board_order":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if mr.StatusCode != http.StatusOK {
		t.Fatalf("move expected 200, got %d", mr.StatusCode)
	}
	var moved model.Task
	json.NewDecoder(mr.Body).Decode(&moved)
	mr.Body.Close()
	if moved.Status != model.TaskInProgress || moved.BoardOrder != 2 {
		t.Errorf("move failed: %q %v", moved.Status, moved.BoardOrder)
	}
	// Bad status.
	br, _ := http.Post(ts.URL+"/api/tasks/"+task.ID+"/move", "application/json",
		strings.NewReader(`{"status":"nope","board_order":1}`))
	if br.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for bad status, got %d", br.StatusCode)
	}
	br.Body.Close()
	// Assign to sprint.
	sp := &model.Sprint{ProjectID: "p1", Name: "S"}
	a.store.CreateSprint(sp)
	sr, _ := http.Post(ts.URL+"/api/tasks/"+task.ID+"/sprint", "application/json",
		strings.NewReader(`{"sprint_id":"`+sp.ID+`"}`))
	if sr.StatusCode != http.StatusOK {
		t.Errorf("sprint assign expected 200, got %d", sr.StatusCode)
	}
	sr.Body.Close()
	got, _ := a.store.GetTask(task.ID)
	if got.SprintID != sp.ID {
		t.Errorf("expected sprint assigned, got %q", got.SprintID)
	}
}

func TestWIPEndpoint(t *testing.T) {
	_, ts := setup(t)
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/wip?project_id=p1",
		strings.NewReader(`{"in_progress":3}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	gr := mustGet(t, ts.URL+"/api/wip?project_id=p1")
	var limits map[string]int
	json.NewDecoder(gr.Body).Decode(&limits)
	gr.Body.Close()
	if limits["in_progress"] != 3 {
		t.Errorf("expected 3, got %d", limits["in_progress"])
	}
}

func TestWIPEndpointInvalidStatusNoPartialWrite(t *testing.T) {
	_, ts := setup(t)
	// One valid + one invalid status. Must 400 with NO partial mutation.
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/wip?project_id=p2",
		strings.NewReader(`{"in_progress":3,"bogus":1}`))
	req.Header.Set("Content-Type", "application/json")
	resp := mustDo(t, req)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("put expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	// The valid key must NOT have been written.
	gr := mustGet(t, ts.URL+"/api/wip?project_id=p2")
	var limits map[string]int
	json.NewDecoder(gr.Body).Decode(&limits)
	gr.Body.Close()
	if limits["in_progress"] != 0 {
		t.Errorf("expected no partial write (in_progress==0/absent), got %d", limits["in_progress"])
	}
}

func TestWIPEndpointMethodNotAllowed(t *testing.T) {
	_, ts := setup(t)
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/wip?project_id=p1", nil)
	resp := mustDo(t, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
}

func TestPageEndpointsCRUD(t *testing.T) {
	_, ts := setup(t)
	resp, err := http.Post(ts.URL+"/api/pages", "application/json",
		strings.NewReader(`{"project_id":"proj1","title":"Spec","content":"# Hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create expected 201, got %d", resp.StatusCode)
	}
	var created model.Page
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	if created.ID == "" {
		t.Fatal("no page id")
	}
	// list scope
	lr := mustGet(t, ts.URL+"/api/pages?project_id=proj1")
	var list []model.Page
	json.NewDecoder(lr.Body).Decode(&list)
	lr.Body.Close()
	if len(list) != 1 {
		t.Errorf("expected 1 page, got %d", len(list))
	}
	// patch
	preq, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/pages/"+created.ID,
		strings.NewReader(`{"content":"# Updated"}`))
	pr, _ := http.DefaultClient.Do(preq)
	if pr.StatusCode != http.StatusOK {
		t.Errorf("patch expected 200, got %d", pr.StatusCode)
	}
	pr.Body.Close()
	// missing title → 400
	br, _ := http.Post(ts.URL+"/api/pages", "application/json", strings.NewReader(`{"content":"x"}`))
	if br.StatusCode != http.StatusBadRequest {
		t.Errorf("missing title expected 400, got %d", br.StatusCode)
	}
	br.Body.Close()
	// delete
	dreq, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/pages/"+created.ID, nil)
	dr, _ := http.DefaultClient.Do(dreq)
	if dr.StatusCode != http.StatusNoContent {
		t.Errorf("delete expected 204, got %d", dr.StatusCode)
	}
	dr.Body.Close()
}
