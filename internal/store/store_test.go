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

func TestTaskDeps(t *testing.T) {
	s := tempStore(t)
	taskA := &model.Task{Title: "Task A"}
	s.CreateTask(taskA)
	taskB := &model.Task{Title: "Task B", DependsOn: []string{taskA.ID}}
	s.CreateTask(taskB)
	taskC := &model.Task{Title: "Task C", DependsOn: []string{taskA.ID}}
	s.CreateTask(taskC)

	// Task A should be blocking B and C
	dependsOn, blocking, err := s.TaskDeps(taskA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(dependsOn) != 0 {
		t.Errorf("expected 0 dependencies, got %d", len(dependsOn))
	}
	if len(blocking) != 2 {
		t.Errorf("expected 2 blocked tasks, got %d", len(blocking))
	}

	// Task B should depend on A
	dependsOn, blocking, err = s.TaskDeps(taskB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(dependsOn) != 1 || dependsOn[0].ID != taskA.ID {
		t.Errorf("expected task B to depend on A")
	}
	if len(blocking) != 0 {
		t.Errorf("expected 0 blocked tasks, got %d", len(blocking))
	}
}

func TestSortTasks(t *testing.T) {
	s := tempStore(t)
	s.CreateTask(&model.Task{Title: "Low task", Priority: model.PriorityLow, Status: model.TaskReady})
	s.CreateTask(&model.Task{Title: "Critical task", Priority: model.PriorityCritical, Status: model.TaskReady})
	s.CreateTask(&model.Task{Title: "High task", Priority: model.PriorityHigh, Status: model.TaskReady})

	// Sort by priority (critical first)
	tasks, _ := s.ListTasks(map[string]string{"sort": "priority"})
	if len(tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(tasks))
	}
	if tasks[0].Priority != model.PriorityCritical {
		t.Errorf("expected critical first, got %s", tasks[0].Priority)
	}
	if tasks[2].Priority != model.PriorityLow {
		t.Errorf("expected low last, got %s", tasks[2].Priority)
	}

	// Sort by title ascending
	tasks, _ = s.ListTasks(map[string]string{"sort": "title"})
	if tasks[0].Title != "Critical task" {
		t.Errorf("expected 'Critical task' first alphabetically, got %s", tasks[0].Title)
	}
}

func TestTaskEvents(t *testing.T) {
	s := tempStore(t)
	task := &model.Task{Title: "History task"}
	s.CreateTask(task)

	s.RecordEvent(&model.Event{Type: model.EventTaskCreated, TaskID: task.ID})
	s.RecordEvent(&model.Event{Type: model.EventTaskClaimed, TaskID: task.ID, AgentID: "agent-1"})
	s.RecordEvent(&model.Event{Type: model.EventAgentJoined, AgentID: "agent-1"}) // unrelated

	events, err := s.ListTaskEvents(task.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Errorf("expected 2 task events, got %d", len(events))
	}
}

func TestSubtaskProgress(t *testing.T) {
	s := tempStore(t)
	parent := &model.Task{Title: "Parent epic"}
	s.CreateTask(parent)

	for _, status := range []model.TaskStatus{model.TaskDone, model.TaskDone, model.TaskInProgress, model.TaskReady} {
		child := &model.Task{Title: "Child", ParentID: parent.ID, Status: status}
		s.CreateTask(child)
	}

	done, total, err := s.SubtaskProgress(parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 {
		t.Errorf("expected 4 total, got %d", total)
	}
	if done != 2 {
		t.Errorf("expected 2 done, got %d", done)
	}
}

func TestComments(t *testing.T) {
	s := tempStore(t)
	task := &model.Task{Title: "Commentable task"}
	s.CreateTask(task)

	// Add comments
	s.AddComment(&model.Comment{TaskID: task.ID, Author: "agent-1", Body: "Started working on this"})
	s.AddComment(&model.Comment{TaskID: task.ID, Author: "agent-2", Body: "Looks good so far"})

	comments, err := s.ListComments(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 2 {
		t.Errorf("expected 2 comments, got %d", len(comments))
	}
	if comments[0].Author != "agent-1" {
		t.Errorf("expected agent-1, got %s", comments[0].Author)
	}
	if comments[1].Body != "Looks good so far" {
		t.Errorf("unexpected body: %s", comments[1].Body)
	}

	// Empty task has no comments
	comments, err = s.ListComments("wg-nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 0 {
		t.Errorf("expected 0 comments, got %d", len(comments))
	}
}

func TestStats(t *testing.T) {
	s := tempStore(t)
	s.CreateTask(&model.Task{Title: "Task 1", Status: model.TaskReady, Priority: model.PriorityHigh})
	s.CreateTask(&model.Task{Title: "Task 2", Status: model.TaskReady, Priority: model.PriorityCritical})
	s.CreateTask(&model.Task{Title: "Task 3", Status: model.TaskDone, Priority: model.PriorityLow})
	s.RegisterAgent("agent-1", "test")

	stats, err := s.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalTasks != 3 {
		t.Errorf("expected 3 tasks, got %d", stats.TotalTasks)
	}
	if stats.TasksByStatus["ready"] != 2 {
		t.Errorf("expected 2 ready tasks, got %d", stats.TasksByStatus["ready"])
	}
	if stats.TasksByPriority["critical"] != 1 {
		t.Errorf("expected 1 critical task, got %d", stats.TasksByPriority["critical"])
	}
	if stats.TotalAgents != 1 {
		t.Errorf("expected 1 agent, got %d", stats.TotalAgents)
	}
}

func TestProjectCRUD(t *testing.T) {
	s := tempStore(t)

	// Create
	p := &model.Project{Name: "Waggle", Description: "Agent orchestration"}
	if err := s.CreateProject(p); err != nil {
		t.Fatal(err)
	}
	if p.ID == "" {
		t.Error("expected project ID to be set")
	}

	// Get
	got, err := s.GetProject(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Waggle" {
		t.Errorf("expected Waggle, got %s", got.Name)
	}

	// List
	s.CreateProject(&model.Project{Name: "Other"})
	projects, err := s.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 2 {
		t.Errorf("expected 2 projects, got %d", len(projects))
	}

	// Update
	updated, err := s.UpdateProject(p.ID, map[string]any{"name": "Waggle v2"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Waggle v2" {
		t.Errorf("expected Waggle v2, got %s", updated.Name)
	}

	// Delete
	if err := s.DeleteProject(p.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.GetProject(p.ID)
	if err != ErrNotFound {
		t.Error("expected not found after delete")
	}
}

func TestTaskTypeAndProject(t *testing.T) {
	s := tempStore(t)

	// Create project
	p := &model.Project{Name: "Auth System"}
	s.CreateProject(p)

	// Create epic under project
	epic := &model.Task{Title: "User Authentication", TaskType: model.TaskTypeEpic, ProjectID: p.ID}
	s.CreateTask(epic)
	if epic.TaskType != model.TaskTypeEpic {
		t.Errorf("expected epic, got %s", epic.TaskType)
	}

	// Create story under epic
	story := &model.Task{
		Title:    "Login flow",
		TaskType: model.TaskTypeStory,
		ParentID: epic.ID,
		ProjectID: p.ID,
		Criteria: []string{"User can log in with email/password", "Error shown on invalid credentials"},
	}
	s.CreateTask(story)

	// Filter by project
	tasks, _ := s.ListTasks(map[string]string{"project_id": p.ID})
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks in project, got %d", len(tasks))
	}

	// Filter by task type
	epics, _ := s.ListTasks(map[string]string{"task_type": "epic", "project_id": p.ID})
	if len(epics) != 1 {
		t.Errorf("expected 1 epic, got %d", len(epics))
	}

	// Subtasks of epic
	subtasks, _ := s.ListSubtasks(epic.ID)
	if len(subtasks) != 1 || subtasks[0].Title != "Login flow" {
		t.Error("expected story as subtask of epic")
	}

	// Task defaults to "task" type
	plain := &model.Task{Title: "Random task"}
	s.CreateTask(plain)
	got, _ := s.GetTask(plain.ID)
	if got.TaskType != model.TaskTypeTask {
		t.Errorf("expected task type 'task', got %s", got.TaskType)
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
