package model

import "time"

type TaskStatus string

const (
	TaskBacklog    TaskStatus = "backlog"
	TaskReady      TaskStatus = "ready"
	TaskInProgress TaskStatus = "in_progress"
	TaskReview     TaskStatus = "review"
	TaskDone       TaskStatus = "done"
	TaskBlocked    TaskStatus = "blocked"
)

func (s TaskStatus) Valid() bool {
	switch s {
	case TaskBacklog, TaskReady, TaskInProgress, TaskReview, TaskDone, TaskBlocked:
		return true
	}
	return false
}

type Priority string

const (
	PriorityCritical Priority = "critical"
	PriorityHigh     Priority = "high"
	PriorityMedium   Priority = "medium"
	PriorityLow      Priority = "low"
)

func (p Priority) Valid() bool {
	switch p {
	case PriorityCritical, PriorityHigh, PriorityMedium, PriorityLow:
		return true
	}
	return false
}

type AgentStatus string

const (
	AgentConnected    AgentStatus = "connected"
	AgentWorking      AgentStatus = "working"
	AgentIdle         AgentStatus = "idle"
	AgentBlocked      AgentStatus = "blocked"
	AgentDisconnected AgentStatus = "disconnected"
)

type Task struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description,omitempty"`
	Criteria    []string   `json:"criteria,omitempty"`
	Status      TaskStatus `json:"status"`
	Priority    Priority   `json:"priority"`
	Assignee    string     `json:"assignee,omitempty"`
	Tags        []string   `json:"tags,omitempty"`
	Estimate    string     `json:"estimate,omitempty"`
	Deadline    *time.Time `json:"deadline,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ParentID    string     `json:"parent_id,omitempty"`
	DependsOn   []string   `json:"depends_on,omitempty"`
	TaskType    TaskType   `json:"task_type,omitempty"`
	ProjectID   string     `json:"project_id,omitempty"`
}

type Agent struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Type        string      `json:"type"`
	Status      AgentStatus `json:"status"`
	CurrentTask string      `json:"current_task,omitempty"`
	ConnectedAt time.Time   `json:"connected_at"`
	LastSeen    time.Time   `json:"last_seen"`
}

type EventType string

const (
	EventTaskCreated         EventType = "task_created"
	EventTaskUpdated         EventType = "task_updated"
	EventTaskClaimed         EventType = "task_claimed"
	EventTaskUnclaimed       EventType = "task_unclaimed"
	EventTaskCompleted       EventType = "task_completed"
	EventTaskDeleted         EventType = "task_deleted"
	EventAgentJoined         EventType = "agent_joined"
	EventAgentLeft           EventType = "agent_left"
	EventAgentStatusChanged  EventType = "agent_status_changed"
	EventMessage             EventType = "message"
)

type Event struct {
	ID        string    `json:"id"`
	Type      EventType `json:"type"`
	AgentID   string    `json:"agent_id,omitempty"`
	TaskID    string    `json:"task_id,omitempty"`
	Payload   any       `json:"payload,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

type TaskType string

const (
	TaskTypeTask  TaskType = "task"
	TaskTypeEpic  TaskType = "epic"
	TaskTypeStory TaskType = "story"
	TaskTypeIssue TaskType = "issue"
)

func (t TaskType) Valid() bool {
	switch t {
	case TaskTypeTask, TaskTypeEpic, TaskTypeStory, TaskTypeIssue:
		return true
	}
	return false
}

type Project struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Comment struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"task_id"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type Message struct {
	ID        string    `json:"id"`
	From      string    `json:"from"`
	To        string    `json:"to,omitempty"`
	Body      string    `json:"body"`
	Read      bool      `json:"read"`
	CreatedAt time.Time `json:"created_at"`
}
