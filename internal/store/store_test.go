package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/maniginam/waggle/internal/model"
)

func tempStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestDefaultPath(t *testing.T) {
	p := DefaultPath()
	home, _ := os.UserHomeDir()
	if p != filepath.Join(home, ".waggle", "waggle.db") {
		t.Errorf("unexpected path: %s", p)
	}
}

func TestCreateAndGetTask(t *testing.T) {
	s := tempStore(t)
	task := &model.Task{Title: "Test task", Priority: model.PriorityHigh}
	if err := s.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	if task.ID == "" {
		t.Error("expected ID to be set")
	}
	if task.Status != model.TaskBacklog {
		t.Errorf("expected backlog, got %s", task.Status)
	}

	got, err := s.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Test task" {
		t.Errorf("expected 'Test task', got %q", got.Title)
	}
	if got.Priority != model.PriorityHigh {
		t.Errorf("expected high, got %s", got.Priority)
	}
}

func TestGetTaskNotFound(t *testing.T) {
	s := tempStore(t)
	_, err := s.GetTask("nonexistent")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestListTasksWithFilters(t *testing.T) {
	s := tempStore(t)
	s.CreateTask(&model.Task{Title: "A", Status: model.TaskReady, Priority: model.PriorityHigh})
	s.CreateTask(&model.Task{Title: "B", Status: model.TaskBacklog, Priority: model.PriorityLow})
	s.CreateTask(&model.Task{Title: "C", Status: model.TaskReady, Priority: model.PriorityMedium})

	tasks, err := s.ListTasks(map[string]string{"status": "ready"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(tasks))
	}

	tasks, err = s.ListTasks(map[string]string{"priority": "high"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Errorf("expected 1 task, got %d", len(tasks))
	}
}

func TestUpdateTask(t *testing.T) {
	s := tempStore(t)
	task := &model.Task{Title: "Original"}
	s.CreateTask(task)

	updated, err := s.UpdateTask(task.ID, map[string]any{"title": "Updated", "status": "ready"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != "Updated" {
		t.Errorf("expected Updated, got %s", updated.Title)
	}
	if updated.Status != model.TaskReady {
		t.Errorf("expected ready, got %s", updated.Status)
	}
}

func TestDeleteTask(t *testing.T) {
	s := tempStore(t)
	task := &model.Task{Title: "Delete me"}
	s.CreateTask(task)

	if err := s.DeleteTask(task.ID); err != nil {
		t.Fatal(err)
	}
	_, err := s.GetTask(task.ID)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestDeleteInProgressTaskRejected(t *testing.T) {
	s := tempStore(t)
	task := &model.Task{Title: "Busy", Status: model.TaskInProgress}
	s.CreateTask(task)

	err := s.DeleteTask(task.ID)
	if err != ErrInProgress {
		t.Errorf("expected ErrInProgress, got %v", err)
	}
}

func TestClaimAndUnclaimTask(t *testing.T) {
	s := tempStore(t)
	task := &model.Task{Title: "Claimable", Status: model.TaskReady}
	s.CreateTask(task)
	s.RegisterAgent("agent-1", "claude-code")

	if err := s.ClaimTask(task.ID, "agent-1"); err != nil {
		t.Fatal(err)
	}

	got, _ := s.GetTask(task.ID)
	if got.Assignee != "agent-1" {
		t.Errorf("expected agent-1, got %s", got.Assignee)
	}
	if got.Status != model.TaskInProgress {
		t.Errorf("expected in_progress, got %s", got.Status)
	}

	// Double claim should fail
	s.RegisterAgent("agent-2", "cursor")
	err := s.ClaimTask(task.ID, "agent-2")
	if err != ErrAlreadyClaimed {
		t.Errorf("expected ErrAlreadyClaimed, got %v", err)
	}

	// Unclaim by wrong agent
	err = s.UnclaimTask(task.ID, "agent-2")
	if err != ErrNotAssigned {
		t.Errorf("expected ErrNotAssigned, got %v", err)
	}

	// Unclaim by correct agent
	if err := s.UnclaimTask(task.ID, "agent-1"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetTask(task.ID)
	if got.Assignee != "" {
		t.Errorf("expected empty assignee, got %s", got.Assignee)
	}
	if got.Status != model.TaskReady {
		t.Errorf("expected ready, got %s", got.Status)
	}
}

func TestCompleteTaskUnblocksDependents(t *testing.T) {
	s := tempStore(t)
	taskA := &model.Task{Title: "A"}
	s.CreateTask(taskA)

	taskB := &model.Task{Title: "B", Status: model.TaskBlocked, DependsOn: []string{taskA.ID}}
	s.CreateTask(taskB)

	if err := s.CompleteTask(taskA.ID); err != nil {
		t.Fatal(err)
	}

	got, _ := s.GetTask(taskB.ID)
	if got.Status != model.TaskReady {
		t.Errorf("expected blocked task to become ready, got %s", got.Status)
	}
}

func TestCyclicDependencyRejected(t *testing.T) {
	s := tempStore(t)
	taskA := &model.Task{Title: "A"}
	s.CreateTask(taskA)
	taskB := &model.Task{Title: "B", DependsOn: []string{taskA.ID}}
	s.CreateTask(taskB)

	// Try to make A depend on B (creates cycle)
	_, err := s.UpdateTask(taskA.ID, map[string]any{"depends_on": []string{taskB.ID}})
	if err != ErrCycleDep {
		t.Errorf("expected ErrCycleDep, got %v", err)
	}
}

func TestRegisterAgent(t *testing.T) {
	s := tempStore(t)
	agent, err := s.RegisterAgent("test-agent", "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if agent.Name != "test-agent" {
		t.Errorf("expected test-agent, got %s", agent.Name)
	}
	if agent.Status != model.AgentConnected {
		t.Errorf("expected connected, got %s", agent.Status)
	}

	// Re-register should upsert
	agent2, err := s.RegisterAgent("test-agent", "cursor")
	if err != nil {
		t.Fatal(err)
	}
	if agent2.Status != model.AgentConnected {
		t.Errorf("expected connected on re-register, got %s", agent2.Status)
	}
}

func TestListAgents(t *testing.T) {
	s := tempStore(t)
	s.RegisterAgent("a1", "claude-code")
	s.RegisterAgent("a2", "cursor")

	agents, err := s.ListAgents("")
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 2 {
		t.Errorf("expected 2 agents, got %d", len(agents))
	}
}

func TestDisconnectAgentUnassignsTasks(t *testing.T) {
	s := tempStore(t)
	s.RegisterAgent("agent-1", "claude-code")
	task := &model.Task{Title: "Work", Status: model.TaskReady}
	s.CreateTask(task)
	s.ClaimTask(task.ID, "agent-1")

	if err := s.DisconnectAgent("agent-1"); err != nil {
		t.Fatal(err)
	}

	got, _ := s.GetTask(task.ID)
	if got.Assignee != "" {
		t.Errorf("expected empty assignee after disconnect, got %s", got.Assignee)
	}
	if got.Status != model.TaskReady {
		t.Errorf("expected ready after disconnect, got %s", got.Status)
	}
}

func TestRecordAndListEvents(t *testing.T) {
	s := tempStore(t)
	e := &model.Event{Type: model.EventTaskCreated, TaskID: "wg-123", Payload: map[string]string{"title": "test"}}
	if err := s.RecordEvent(e); err != nil {
		t.Fatal(err)
	}

	events, err := s.ListEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Errorf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != model.EventTaskCreated {
		t.Errorf("expected task_created, got %s", events[0].Type)
	}
}

func TestSendAndReadMessages(t *testing.T) {
	s := tempStore(t)
	msg := &model.Message{From: "agent-1", To: "agent-2", Body: "hello"}
	if err := s.SendMessage(msg); err != nil {
		t.Fatal(err)
	}

	msgs, err := s.ReadMessages("agent-2", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Errorf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Body != "hello" {
		t.Errorf("expected 'hello', got %s", msgs[0].Body)
	}

	// Messages should be marked as read now
	msgs2, _ := s.ReadMessages("agent-2", 10)
	if len(msgs2) > 0 && !msgs2[0].Read {
		t.Error("expected message to be marked as read")
	}
}

func TestSearchTasks(t *testing.T) {
	s := tempStore(t)
	s.CreateTask(&model.Task{Title: "Build authentication module"})
	s.CreateTask(&model.Task{Title: "Fix login bug", Description: "Authentication fails on timeout"})
	s.CreateTask(&model.Task{Title: "Write docs"})

	// Search by title
	tasks, _ := s.ListTasks(map[string]string{"q": "auth"})
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks matching 'auth', got %d", len(tasks))
	}

	// Search by description
	tasks, _ = s.ListTasks(map[string]string{"q": "timeout"})
	if len(tasks) != 1 {
		t.Errorf("expected 1 task matching 'timeout', got %d", len(tasks))
	}

	// No matches
	tasks, _ = s.ListTasks(map[string]string{"q": "nonexistent"})
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(tasks))
	}
}

func TestBroadcastMessage(t *testing.T) {
	s := tempStore(t)
	msg := &model.Message{From: "agent-1", To: "", Body: "broadcast"}
	s.SendMessage(msg)

	// Any agent should see broadcasts
	msgs, _ := s.ReadMessages("agent-2", 10)
	if len(msgs) != 1 {
		t.Errorf("expected 1 broadcast message, got %d", len(msgs))
	}
}
