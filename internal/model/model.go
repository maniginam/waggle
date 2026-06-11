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
	IssueNumber int        `json:"issue_number,omitempty"`
	IssueURL    string     `json:"issue_url,omitempty"`
}

type AgentRole string

const (
	AgentRoleAlpha  AgentRole = "alpha"
	AgentRoleLeader AgentRole = "leader"
	AgentRoleWorker AgentRole = "worker"
)

type Agent struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Type        string      `json:"type"`
	Role        AgentRole   `json:"role"`
	Status      AgentStatus `json:"status"`
	CurrentTask string      `json:"current_task,omitempty"`
	ProjectID   string      `json:"project_id,omitempty"`
	ParentAgent string      `json:"parent_agent,omitempty"`
	PersonaID   string      `json:"persona_id,omitempty"`
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
	EventAgentStale          EventType = "agent_stale"
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
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Description   string        `json:"description,omitempty"`
	LeaderAgent   string        `json:"leader_agent,omitempty"`
	AutoDispatch  bool          `json:"auto_dispatch"`
	WorkDir       string        `json:"work_dir,omitempty"`
	Status        ProjectStatus `json:"status,omitempty"`
	Account       string        `json:"account,omitempty"`
	Category      string        `json:"category,omitempty"`
	LastTouchedAt *time.Time    `json:"last_touched_at,omitempty"`
	ParkingNote   string        `json:"parking_note,omitempty"`
	Health        ProjectHealth `json:"health,omitempty"`
	RevenueStatus string        `json:"revenue_status,omitempty"`
	TechStack     string        `json:"tech_stack,omitempty"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
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
	ProjectID string    `json:"project_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// --- Context Manager types ---

type ProjectStatus string

const (
	ProjectActive       ProjectStatus = "active"
	ProjectDormant      ProjectStatus = "dormant"
	ProjectPaused       ProjectStatus = "paused"
	ProjectEarning      ProjectStatus = "earning"
	ProjectBroken       ProjectStatus = "broken"
	ProjectKilled       ProjectStatus = "killed"
)

func (s ProjectStatus) Valid() bool {
	switch s {
	case ProjectActive, ProjectDormant, ProjectPaused, ProjectEarning, ProjectBroken, ProjectKilled:
		return true
	}
	return false
}

type ProjectHealth string

const (
	HealthGreen   ProjectHealth = "green"
	HealthYellow  ProjectHealth = "yellow"
	HealthRed     ProjectHealth = "red"
	HealthUnknown ProjectHealth = "unknown"
)

type Session struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	Summary   string    `json:"summary,omitempty"`
	Account   string    `json:"account,omitempty"`
}

type Progress struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	Source    string    `json:"source"`
	Summary   string    `json:"summary"`
	Detail    string    `json:"detail,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

