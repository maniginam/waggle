package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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
	s.RegisterAgent("agent-1", "claude-code", "", "", "")

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
	s.RegisterAgent("agent-2", "cursor", "", "", "")
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
	agent, err := s.RegisterAgent("test-agent", "claude-code", "", "", "")
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
	agent2, err := s.RegisterAgent("test-agent", "cursor", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if agent2.Status != model.AgentConnected {
		t.Errorf("expected connected on re-register, got %s", agent2.Status)
	}
}

func TestListAgents(t *testing.T) {
	s := tempStore(t)
	s.RegisterAgent("a1", "claude-code", "", "", "")
	s.RegisterAgent("a2", "cursor", "", "", "")

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
	s.RegisterAgent("agent-1", "claude-code", "", "", "")
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
	s.RegisterAgent("agent-1", "test", "", "", "")

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

func TestSettingsGetSet(t *testing.T) {
	s := tempStore(t)

	// Get non-existent setting returns empty
	val, err := s.GetSetting("theme")
	if err != nil {
		t.Fatal(err)
	}
	if val != "" {
		t.Errorf("expected empty string for missing setting, got %q", val)
	}

	// Set a setting
	if err := s.SetSetting("theme", "dark"); err != nil {
		t.Fatal(err)
	}

	// Get it back
	val, err = s.GetSetting("theme")
	if err != nil {
		t.Fatal(err)
	}
	if val != "dark" {
		t.Errorf("expected 'dark', got %q", val)
	}

	// Update it
	if err := s.SetSetting("theme", "light"); err != nil {
		t.Fatal(err)
	}
	val, _ = s.GetSetting("theme")
	if val != "light" {
		t.Errorf("expected 'light', got %q", val)
	}
}

func TestReviewCRUD(t *testing.T) {
	s := tempStore(t)
	task := &model.Task{Title: "Review target"}
	s.CreateTask(task)

	r := &model.Review{
		TaskID:  task.ID,
		AgentID: "reviewer-1",
		Branch:  "feature/auth",
		Diff:    "+ added auth\n- removed old",
		Summary: "Auth implementation",
	}
	if err := s.CreateReview(r); err != nil {
		t.Fatal(err)
	}
	if r.ID == "" {
		t.Error("expected review ID")
	}
	if r.Status != model.ReviewPending {
		t.Errorf("expected pending, got %s", r.Status)
	}

	// Get
	got, err := s.GetReview(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary != "Auth implementation" {
		t.Errorf("expected summary, got %q", got.Summary)
	}

	// List by task
	reviews, err := s.ListReviewsByTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(reviews) != 1 {
		t.Errorf("expected 1 review, got %d", len(reviews))
	}

	// Update status
	if err := s.UpdateReviewStatus(r.ID, model.ReviewApproved, "LGTM"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetReview(r.ID)
	if got.Status != model.ReviewApproved {
		t.Errorf("expected approved, got %s", got.Status)
	}
	if got.Feedback != "LGTM" {
		t.Errorf("expected LGTM, got %q", got.Feedback)
	}

	// List by status
	approved, _ := s.ListReviews("approved")
	if len(approved) != 1 {
		t.Errorf("expected 1 approved review, got %d", len(approved))
	}
	pending, _ := s.ListReviews("pending")
	if len(pending) != 0 {
		t.Errorf("expected 0 pending reviews, got %d", len(pending))
	}
}

func TestTokenUsage(t *testing.T) {
	s := tempStore(t)

	u1 := &model.TokenUsage{
		AgentName:    "agent-1",
		Model:        "sonnet",
		InputTokens:  1000,
		OutputTokens: 500,
		TaskID:       "wg-123",
	}
	if err := s.RecordTokenUsage(u1); err != nil {
		t.Fatal(err)
	}
	if u1.TotalTokens != 1500 {
		t.Errorf("expected 1500 total, got %d", u1.TotalTokens)
	}
	if u1.CostUSD == 0 {
		t.Error("expected cost to be calculated")
	}

	u2 := &model.TokenUsage{
		AgentName:    "agent-2",
		Model:        "opus",
		InputTokens:  2000,
		OutputTokens: 1000,
	}
	s.RecordTokenUsage(u2)

	// By agent
	byAgent, err := s.TokenUsageByAgent()
	if err != nil {
		t.Fatal(err)
	}
	if len(byAgent) != 2 {
		t.Errorf("expected 2 agent summaries, got %d", len(byAgent))
	}

	// Total
	total, err := s.TokenUsageTotal()
	if err != nil {
		t.Fatal(err)
	}
	if total.TotalTokens != 4500 {
		t.Errorf("expected 4500 total tokens, got %d", total.TotalTokens)
	}
	if total.Reports != 2 {
		t.Errorf("expected 2 reports, got %d", total.Reports)
	}

	// Recent
	recent, err := s.TokenUsageRecent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 2 {
		t.Errorf("expected 2 recent, got %d", len(recent))
	}
}

func TestPushSubscriptions(t *testing.T) {
	s := tempStore(t)

	sub := &PushSubscription{
		Endpoint: "https://push.example.com/sub1",
		Auth:     "auth-key-1",
		P256dh:   "p256dh-key-1",
	}
	if err := s.SavePushSubscription(sub); err != nil {
		t.Fatal(err)
	}

	subs, err := s.ListPushSubscriptions()
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 {
		t.Errorf("expected 1 subscription, got %d", len(subs))
	}
	if subs[0].Endpoint != "https://push.example.com/sub1" {
		t.Errorf("unexpected endpoint: %s", subs[0].Endpoint)
	}

	// Upsert same endpoint
	sub2 := &PushSubscription{
		Endpoint: "https://push.example.com/sub1",
		Auth:     "new-auth",
		P256dh:   "new-p256dh",
	}
	s.SavePushSubscription(sub2)
	subs, _ = s.ListPushSubscriptions()
	if len(subs) != 1 {
		t.Errorf("expected 1 after upsert, got %d", len(subs))
	}

	// Delete
	if err := s.DeletePushSubscription("https://push.example.com/sub1"); err != nil {
		t.Fatal(err)
	}
	subs, _ = s.ListPushSubscriptions()
	if len(subs) != 0 {
		t.Errorf("expected 0 after delete, got %d", len(subs))
	}
}

func TestMarkMessagesRead(t *testing.T) {
	s := tempStore(t)
	m1 := &model.Message{From: "a", To: "b", Body: "hello"}
	m2 := &model.Message{From: "a", To: "b", Body: "world"}
	s.SendMessage(m1)
	s.SendMessage(m2)

	// Mark specific IDs
	if err := s.MarkMessagesRead([]string{m1.ID}); err != nil {
		t.Fatal(err)
	}

	// Mark all
	if err := s.MarkAllMessagesRead(); err != nil {
		t.Fatal(err)
	}

	// Empty list is no-op
	if err := s.MarkMessagesRead(nil); err != nil {
		t.Fatal(err)
	}
}

func TestListAllMessages(t *testing.T) {
	s := tempStore(t)
	s.SendMessage(&model.Message{From: "a", To: "b", Body: "msg1"})
	s.SendMessage(&model.Message{From: "c", To: "d", Body: "msg2"})

	msgs, err := s.ListAllMessages(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Errorf("expected 2 messages, got %d", len(msgs))
	}
}

func TestAgentRoles(t *testing.T) {
	s := tempStore(t)

	// Register with role
	a, err := s.RegisterAgent("lead-1", "claude-code", "proj-1", model.AgentRoleLeader, "alpha-1")
	if err != nil {
		t.Fatal(err)
	}
	if a.Role != model.AgentRoleLeader {
		t.Errorf("expected leader role, got %s", a.Role)
	}
	if a.ParentAgent != "alpha-1" {
		t.Errorf("expected parent alpha-1, got %s", a.ParentAgent)
	}

	// Default role
	a2, _ := s.RegisterAgent("worker-1", "claude-code", "", "", "")
	if a2.Role != model.AgentRoleWorker {
		t.Errorf("expected worker role, got %s", a2.Role)
	}
}

func TestPurgeStaleAgents(t *testing.T) {
	s := tempStore(t)
	s.RegisterAgent("old-agent", "test", "", "", "")
	s.DisconnectAgent("old-agent")

	// Set last_seen to 48 hours ago
	old := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339)
	s.db.Exec("UPDATE agents SET last_seen = ? WHERE name = ?", old, "old-agent")

	s.RegisterAgent("fresh-agent", "test", "", "", "")
	s.DisconnectAgent("fresh-agent")

	purged, err := s.PurgeStaleAgents(24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if purged != 1 {
		t.Errorf("expected 1 purged, got %d", purged)
	}

	agents, _ := s.ListAgents("")
	if len(agents) != 1 {
		t.Errorf("expected 1 remaining, got %d", len(agents))
	}
}

func TestDeleteAgent(t *testing.T) {
	s := tempStore(t)
	s.RegisterAgent("del-me", "test", "", "", "")
	if err := s.DeleteAgent("del-me"); err != nil {
		t.Fatal(err)
	}
	agents, _ := s.ListAgents("")
	if len(agents) != 0 {
		t.Errorf("expected 0 agents after delete, got %d", len(agents))
	}
}

func TestUpdateAgentProject(t *testing.T) {
	s := tempStore(t)
	s.RegisterAgent("proj-agent", "test", "", "", "")
	if err := s.UpdateAgentProject("proj-agent", "proj-123"); err != nil {
		t.Fatal(err)
	}
	a, _ := s.GetAgentByName("proj-agent")
	if a.ProjectID != "proj-123" {
		t.Errorf("expected proj-123, got %s", a.ProjectID)
	}
}

func TestSettingsGetAll(t *testing.T) {
	s := tempStore(t)

	s.SetSetting("theme", "dark")
	s.SetSetting("sound", "on")
	s.SetSetting("refresh_interval", "30")

	settings, err := s.GetAllSettings()
	if err != nil {
		t.Fatal(err)
	}
	if len(settings) != 3 {
		t.Errorf("expected 3 settings, got %d", len(settings))
	}
	if settings["theme"] != "dark" {
		t.Errorf("expected dark theme, got %q", settings["theme"])
	}
}
