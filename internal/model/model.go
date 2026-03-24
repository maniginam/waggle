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
	ProjectID   string      `json:"project_id,omitempty"`
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

type ReviewStatus string

const (
	ReviewPending  ReviewStatus = "pending"
	ReviewApproved ReviewStatus = "approved"
	ReviewRejected ReviewStatus = "rejected"
)

type Review struct {
	ID        string       `json:"id"`
	TaskID    string       `json:"task_id"`
	AgentID   string       `json:"agent_id"`
	Branch    string       `json:"branch,omitempty"`
	Diff      string       `json:"diff"`
	Summary   string       `json:"summary,omitempty"`
	Status    ReviewStatus `json:"status"`
	Feedback  string       `json:"feedback,omitempty"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}

type TokenUsage struct {
	ID           string    `json:"id"`
	AgentName    string    `json:"agent_name"`
	Model        string    `json:"model"`
	InputTokens  int64     `json:"input_tokens"`
	OutputTokens int64     `json:"output_tokens"`
	TotalTokens  int64     `json:"total_tokens"`
	CostUSD      float64   `json:"cost_usd"`
	TaskID       string    `json:"task_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type TokenSummary struct {
	AgentName    string  `json:"agent_name"`
	Model        string  `json:"model,omitempty"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	TotalTokens  int64   `json:"total_tokens"`
	CostUSD      float64 `json:"cost_usd"`
	Reports      int     `json:"reports"`
}

// CostPerMillion defines token pricing per model (USD per million tokens)
var ModelPricing = map[string][2]float64{
	"claude-opus-4-6":          {15.0, 75.0},
	"claude-sonnet-4-6":        {3.0, 15.0},
	"claude-haiku-4-5-20251001": {0.25, 1.25},
	// Aliases
	"opus":   {15.0, 75.0},
	"sonnet": {3.0, 15.0},
	"haiku":  {0.25, 1.25},
}

func CalculateCost(model string, inputTokens, outputTokens int64) float64 {
	pricing, ok := ModelPricing[model]
	if !ok {
		// Default to sonnet pricing
		pricing = ModelPricing["sonnet"]
	}
	inputCost := float64(inputTokens) / 1_000_000.0 * pricing[0]
	outputCost := float64(outputTokens) / 1_000_000.0 * pricing[1]
	return inputCost + outputCost
}
