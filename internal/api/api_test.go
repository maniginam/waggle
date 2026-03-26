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

func TestMessagesListAll(t *testing.T) {
	_, ts := setup(t)
	resp, _ := http.Get(ts.URL + "/api/messages")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for listing all messages, got %d", resp.StatusCode)
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

func TestTaskDepsAPI(t *testing.T) {
	_, ts := setup(t)

	// Create tasks with dependency
	body, _ := json.Marshal(map[string]string{"title": "Dep parent"})
	resp, _ := http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(body))
	var parentTask map[string]any
	json.NewDecoder(resp.Body).Decode(&parentTask)
	resp.Body.Close()
	parentID := parentTask["id"].(string)

	body, _ = json.Marshal(map[string]any{"title": "Dep child", "depends_on": []string{parentID}})
	resp, _ = http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(body))
	resp.Body.Close()

	resp, _ = http.Get(ts.URL + "/api/tasks/" + parentID + "/deps")
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
	resp, _ := http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(body))
	var task map[string]any
	json.NewDecoder(resp.Body).Decode(&task)
	resp.Body.Close()
	taskID := task["id"].(string)

	// Get history
	resp, _ = http.Get(ts.URL + "/api/tasks/" + taskID + "/history")
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
	resp, _ := http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(body))
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

	resp, _ = http.Get(ts.URL + "/api/tasks/" + parentID + "/subtasks")
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
	resp, _ := http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(body))
	var task map[string]any
	json.NewDecoder(resp.Body).Decode(&task)
	resp.Body.Close()
	taskID := task["id"].(string)

	// Add a comment
	commentBody, _ := json.Marshal(map[string]string{"author": "test-agent", "body": "Working on it"})
	resp, _ = http.Post(ts.URL+"/api/tasks/"+taskID+"/comments", "application/json", bytes.NewBuffer(commentBody))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// List comments
	resp, _ = http.Get(ts.URL + "/api/tasks/" + taskID + "/comments")
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
	resp, _ := http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(body))
	var task map[string]any
	json.NewDecoder(resp.Body).Decode(&task)
	resp.Body.Close()
	taskID := task["id"].(string)

	// Missing author
	commentBody, _ := json.Marshal(map[string]string{"body": "no author"})
	resp, _ = http.Post(ts.URL+"/api/tasks/"+taskID+"/comments", "application/json", bytes.NewBuffer(commentBody))
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

	resp, _ := http.Get(ts.URL + "/api/stats")
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

	resp, _ := http.Get(ts.URL + "/api/tasks?q=auth")
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
	resp, _ := http.Post(ts.URL+"/api/projects", "application/json", bytes.NewBufferString(body))
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
	resp, _ = http.Get(ts.URL + "/api/projects/" + projectID)
	var got map[string]any
	json.NewDecoder(resp.Body).Decode(&got)
	resp.Body.Close()
	if got["name"] != "Auth System" {
		t.Errorf("expected Auth System, got %v", got["name"])
	}

	// List projects
	resp, _ = http.Get(ts.URL + "/api/projects")
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
	resp, _ = http.DefaultClient.Do(req)
	var updated map[string]any
	json.NewDecoder(resp.Body).Decode(&updated)
	resp.Body.Close()
	if updated["name"] != "Auth System v2" {
		t.Errorf("expected Auth System v2, got %v", updated["name"])
	}

	// Delete project
	req, _ = http.NewRequest(http.MethodDelete, ts.URL+"/api/projects/"+projectID, nil)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", resp.StatusCode)
	}

	// Verify deleted
	resp, _ = http.Get(ts.URL + "/api/projects/" + projectID)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", resp.StatusCode)
	}
}

func TestProjectEpicsAPI(t *testing.T) {
	_, ts := setup(t)

	// Create project
	resp, _ := http.Post(ts.URL+"/api/projects", "application/json",
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
	resp, _ = http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewBuffer(epicBody))
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
	resp, _ = http.Get(ts.URL + "/api/projects/" + projectID + "/epics")
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
	resp, _ := http.Post(ts.URL+"/api/projects", "application/json",
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
	resp, _ := http.Post(ts.URL+"/api/projects", "application/json", bytes.NewBuffer(body))
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
	resp, _ = http.Get(ts.URL + "/api/projects")
	var plain []map[string]any
	json.NewDecoder(resp.Body).Decode(&plain)
	resp.Body.Close()
	if _, hasCounts := plain[0]["task_count"]; hasCounts {
		t.Error("expected no task_count without counts=true")
	}

	// Fetch with counts
	resp, _ = http.Get(ts.URL + "/api/projects?counts=true")
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
	resp, _ := http.Get(ts.URL + "/api/tasks?task_type=epic")
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
	resp, _ := http.Get(ts.URL + "/api/settings")
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
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// GET settings back
	resp, _ = http.Get(ts.URL + "/api/settings")
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

	resp, _ := http.Get(ts.URL + "/api/tasks/export?format=json")
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

	resp, _ := http.Get(ts.URL + "/api/tasks/export?format=csv")
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
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
}
