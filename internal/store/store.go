package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/maniginam/waggle/internal/model"
	"github.com/maniginam/waggle/pkg/id"
	_ "modernc.org/sqlite"
)

var (
	ErrNotFound       = errors.New("not found")
	ErrAlreadyClaimed = errors.New("task already claimed")
	ErrNotAssigned    = errors.New("task not assigned to this agent")
	ErrInProgress     = errors.New("cannot delete in-progress task")
	ErrCycleDep       = errors.New("circular dependency detected")
)

type Store struct {
	db *sql.DB
}

func DefaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".waggle", "waggle.db")
}

func New(dbPath string) (*Store, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create dir: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL&_cache_size=-20000")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	// Connection pool: single writer, multiple readers
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS tasks (
			id          TEXT PRIMARY KEY,
			title       TEXT NOT NULL,
			description TEXT DEFAULT '',
			criteria    TEXT DEFAULT '[]',
			status      TEXT DEFAULT 'backlog',
			priority    TEXT DEFAULT 'medium',
			assignee    TEXT DEFAULT '',
			tags        TEXT DEFAULT '[]',
			estimate    TEXT DEFAULT '',
			deadline    TEXT DEFAULT '',
			created_at  TEXT NOT NULL,
			updated_at  TEXT NOT NULL,
			parent_id   TEXT DEFAULT '',
			depends_on  TEXT DEFAULT '[]',
			task_type   TEXT DEFAULT 'task',
			project_id  TEXT DEFAULT ''
		);

		CREATE TABLE IF NOT EXISTS agents (
			id           TEXT PRIMARY KEY,
			name         TEXT UNIQUE NOT NULL,
			type         TEXT DEFAULT 'custom',
			status       TEXT DEFAULT 'connected',
			current_task TEXT DEFAULT '',
			connected_at TEXT NOT NULL,
			last_seen    TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS events (
			id        TEXT PRIMARY KEY,
			type      TEXT NOT NULL,
			agent_id  TEXT DEFAULT '',
			task_id   TEXT DEFAULT '',
			payload   TEXT DEFAULT '{}',
			timestamp TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS messages (
			id         TEXT PRIMARY KEY,
			"from"     TEXT NOT NULL,
			"to"       TEXT DEFAULT '',
			body       TEXT NOT NULL,
			read       INTEGER DEFAULT 0,
			created_at TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS comments (
			id         TEXT PRIMARY KEY,
			task_id    TEXT NOT NULL,
			author     TEXT NOT NULL,
			body       TEXT NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
		);

		CREATE TABLE IF NOT EXISTS projects (
			id          TEXT PRIMARY KEY,
			name        TEXT NOT NULL,
			description TEXT DEFAULT '',
			created_at  TEXT NOT NULL,
			updated_at  TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS token_usage (
			id            TEXT PRIMARY KEY,
			agent_name    TEXT NOT NULL,
			model         TEXT DEFAULT '',
			input_tokens  INTEGER DEFAULT 0,
			output_tokens INTEGER DEFAULT 0,
			total_tokens  INTEGER DEFAULT 0,
			cost_usd      REAL DEFAULT 0,
			task_id       TEXT DEFAULT '',
			created_at    TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS reviews (
			id         TEXT PRIMARY KEY,
			task_id    TEXT NOT NULL,
			agent_id   TEXT NOT NULL,
			branch     TEXT DEFAULT '',
			diff       TEXT NOT NULL,
			summary    TEXT DEFAULT '',
			status     TEXT DEFAULT 'pending',
			feedback   TEXT DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS push_subscriptions (
			id         TEXT PRIMARY KEY,
			endpoint   TEXT UNIQUE NOT NULL,
			auth       TEXT NOT NULL,
			p256dh     TEXT NOT NULL,
			created_at TEXT NOT NULL
		);

		CREATE INDEX IF NOT EXISTS idx_reviews_task ON reviews(task_id);
		CREATE INDEX IF NOT EXISTS idx_reviews_status ON reviews(status);
		CREATE INDEX IF NOT EXISTS idx_comments_task ON comments(task_id);
		CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
		CREATE INDEX IF NOT EXISTS idx_tasks_assignee ON tasks(assignee);
		CREATE INDEX IF NOT EXISTS idx_agents_name ON agents(name);
		CREATE INDEX IF NOT EXISTS idx_events_timestamp ON events(timestamp);
		CREATE INDEX IF NOT EXISTS idx_messages_to ON messages("to");
		CREATE INDEX IF NOT EXISTS idx_token_usage_agent ON token_usage(agent_name);
		CREATE INDEX IF NOT EXISTS idx_token_usage_time ON token_usage(created_at);
	`)
	if err != nil {
		return err
	}

	// Add columns to existing tables (safe to re-run — ALTER TABLE IF NOT EXISTS column not supported in SQLite, so we check)
	for _, col := range []struct{ table, name, def string }{
		{"tasks", "task_type", "TEXT DEFAULT 'task'"},
		{"tasks", "project_id", "TEXT DEFAULT ''"},
		{"agents", "project_id", "TEXT DEFAULT ''"},
		{"tasks", "issue_number", "INTEGER DEFAULT 0"},
		{"tasks", "issue_url", "TEXT DEFAULT ''"},
		{"agents", "role", "TEXT DEFAULT 'worker'"},
		{"agents", "parent_agent", "TEXT DEFAULT ''"},
		{"projects", "leader_agent", "TEXT DEFAULT ''"},
	} {
		var count int
		s.db.QueryRow("SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?", col.table, col.name).Scan(&count)
		if count == 0 {
			s.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", col.table, col.name, col.def))
		}
	}

	// Create indexes that depend on migrated columns (must run after ALTER TABLE)
	s.db.Exec("CREATE INDEX IF NOT EXISTS idx_tasks_project ON tasks(project_id)")
	return err
}

// --- Tasks ---

func (s *Store) CreateTask(t *model.Task) error {
	if t.ID == "" {
		t.ID = id.New()
	}
	now := time.Now().UTC()
	t.CreatedAt = now
	t.UpdatedAt = now
	if t.Status == "" {
		t.Status = model.TaskBacklog
	}
	if t.Priority == "" {
		t.Priority = model.PriorityMedium
	}
	if t.TaskType == "" {
		t.TaskType = model.TaskTypeTask
	}

	if len(t.DependsOn) > 0 {
		if err := s.checkCycleDeps(t.ID, t.DependsOn); err != nil {
			return err
		}
	}

	criteria, _ := json.Marshal(t.Criteria)
	tags, _ := json.Marshal(t.Tags)
	dependsOn, _ := json.Marshal(t.DependsOn)
	deadline := ""
	if t.Deadline != nil {
		deadline = t.Deadline.Format(time.RFC3339)
	}

	_, err := s.db.Exec(`INSERT INTO tasks (id, title, description, criteria, status, priority, assignee, tags, estimate, deadline, created_at, updated_at, parent_id, depends_on, task_type, project_id, issue_number, issue_url)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Title, t.Description, string(criteria), string(t.Status), string(t.Priority),
		t.Assignee, string(tags), t.Estimate, deadline,
		t.CreatedAt.Format(time.RFC3339), t.UpdatedAt.Format(time.RFC3339),
		t.ParentID, string(dependsOn), string(t.TaskType), t.ProjectID,
		t.IssueNumber, t.IssueURL)
	return err
}

func (s *Store) GetTask(id string) (*model.Task, error) {
	row := s.db.QueryRow(`SELECT id, title, description, criteria, status, priority, assignee, tags, estimate, deadline, created_at, updated_at, parent_id, depends_on, task_type, project_id, issue_number, issue_url FROM tasks WHERE id = ?`, id)
	return scanTask(row)
}

func (s *Store) ListTasks(filters map[string]string) ([]*model.Task, error) {
	query := `SELECT id, title, description, criteria, status, priority, assignee, tags, estimate, deadline, created_at, updated_at, parent_id, depends_on, task_type, project_id, issue_number, issue_url FROM tasks`
	var conditions []string
	var args []any

	if v, ok := filters["status"]; ok {
		conditions = append(conditions, "status = ?")
		args = append(args, v)
	}
	if v, ok := filters["assignee"]; ok {
		conditions = append(conditions, "assignee = ?")
		args = append(args, v)
	}
	if v, ok := filters["priority"]; ok {
		conditions = append(conditions, "priority = ?")
		args = append(args, v)
	}
	if v, ok := filters["tag"]; ok {
		conditions = append(conditions, "tags LIKE ?")
		args = append(args, "%"+v+"%")
	}
	if v, ok := filters["parent_id"]; ok {
		conditions = append(conditions, "parent_id = ?")
		args = append(args, v)
	}
	if v, ok := filters["project_id"]; ok {
		conditions = append(conditions, "project_id = ?")
		args = append(args, v)
	}
	if v, ok := filters["task_type"]; ok {
		conditions = append(conditions, "task_type = ?")
		args = append(args, v)
	}
	if v, ok := filters["q"]; ok {
		conditions = append(conditions, "(title LIKE ? OR description LIKE ?)")
		args = append(args, "%"+v+"%", "%"+v+"%")
	}

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}

	// Sorting
	sortCol := "created_at"
	sortDir := "DESC"
	if v, ok := filters["sort"]; ok {
		switch v {
		case "priority":
			// Use CASE to order critical > high > medium > low
			sortCol = "CASE priority WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 WHEN 'low' THEN 3 ELSE 4 END"
			sortDir = "ASC"
		case "deadline":
			sortCol = "deadline"
			sortDir = "ASC"
		case "updated":
			sortCol = "updated_at"
		case "title":
			sortCol = "title"
			sortDir = "ASC"
		case "status":
			sortCol = "status"
			sortDir = "ASC"
		}
	}
	if v, ok := filters["order"]; ok && (v == "asc" || v == "desc") {
		sortDir = strings.ToUpper(v)
	}
	query += " ORDER BY " + sortCol + " " + sortDir

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []*model.Task
	for rows.Next() {
		t, err := scanTaskRows(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

func (s *Store) UpdateTask(id string, updates map[string]any) (*model.Task, error) {
	task, err := s.GetTask(id)
	if err != nil {
		return nil, err
	}

	var sets []string
	var args []any

	for k, v := range updates {
		switch k {
		case "title":
			sets = append(sets, "title = ?")
			args = append(args, v)
		case "description":
			sets = append(sets, "description = ?")
			args = append(args, v)
		case "status":
			sets = append(sets, "status = ?")
			args = append(args, v)
		case "priority":
			sets = append(sets, "priority = ?")
			args = append(args, v)
		case "assignee":
			sets = append(sets, "assignee = ?")
			args = append(args, v)
		case "estimate":
			sets = append(sets, "estimate = ?")
			args = append(args, v)
		case "criteria":
			b, _ := json.Marshal(v)
			sets = append(sets, "criteria = ?")
			args = append(args, string(b))
		case "tags":
			b, _ := json.Marshal(v)
			sets = append(sets, "tags = ?")
			args = append(args, string(b))
		case "depends_on":
			deps, ok := v.([]string)
			if ok {
				if err := s.checkCycleDeps(task.ID, deps); err != nil {
					return nil, err
				}
			}
			b, _ := json.Marshal(v)
			sets = append(sets, "depends_on = ?")
			args = append(args, string(b))
		case "task_type":
			sets = append(sets, "task_type = ?")
			args = append(args, v)
		case "project_id":
			sets = append(sets, "project_id = ?")
			args = append(args, v)
		case "issue_number":
			sets = append(sets, "issue_number = ?")
			args = append(args, v)
		case "issue_url":
			sets = append(sets, "issue_url = ?")
			args = append(args, v)
		}
	}

	if len(sets) == 0 {
		return task, nil
	}

	sets = append(sets, "updated_at = ?")
	args = append(args, time.Now().UTC().Format(time.RFC3339))
	args = append(args, id)

	_, err = s.db.Exec("UPDATE tasks SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...)
	if err != nil {
		return nil, err
	}
	return s.GetTask(id)
}

func (s *Store) DeleteTask(id string) error {
	task, err := s.GetTask(id)
	if err != nil {
		return err
	}
	if task.Status == model.TaskInProgress {
		return ErrInProgress
	}
	_, err = s.db.Exec("DELETE FROM tasks WHERE id = ?", id)
	return err
}

func (s *Store) ClaimTask(taskID, agentName string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var assignee string
	err = tx.QueryRow("SELECT assignee FROM tasks WHERE id = ?", taskID).Scan(&assignee)
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if assignee != "" {
		return ErrAlreadyClaimed
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, err = tx.Exec("UPDATE tasks SET assignee = ?, status = 'in_progress', updated_at = ? WHERE id = ?",
		agentName, now, taskID)
	if err != nil {
		return err
	}
	_, err = tx.Exec("UPDATE agents SET current_task = ?, status = 'working', last_seen = ? WHERE name = ?",
		taskID, now, agentName)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UnclaimTask(taskID, agentName string) error {
	task, err := s.GetTask(taskID)
	if err != nil {
		return err
	}
	if task.Assignee != agentName {
		return ErrNotAssigned
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.Exec("UPDATE tasks SET assignee = '', status = 'ready', updated_at = ? WHERE id = ?", now, taskID)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("UPDATE agents SET current_task = '', status = 'idle', last_seen = ? WHERE name = ?", now, agentName)
	return err
}

func (s *Store) CompleteTask(taskID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec("UPDATE tasks SET status = 'done', updated_at = ? WHERE id = ?", now, taskID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}

	// Unblock dependents — collect first, then process (avoid nested queries with open cursor)
	rows, err := s.db.Query("SELECT id, depends_on FROM tasks WHERE status = 'blocked'")
	if err != nil {
		return err
	}

	type blocked struct {
		id   string
		deps []string
	}
	var candidates []blocked
	for rows.Next() {
		var b blocked
		var depsJSON string
		if err := rows.Scan(&b.id, &depsJSON); err != nil {
			continue
		}
		json.Unmarshal([]byte(depsJSON), &b.deps)
		candidates = append(candidates, b)
	}
	rows.Close()

	for _, b := range candidates {
		allDone := true
		for _, d := range b.deps {
			if d == taskID {
				continue
			}
			var st string
			s.db.QueryRow("SELECT status FROM tasks WHERE id = ?", d).Scan(&st)
			if st != string(model.TaskDone) {
				allDone = false
				break
			}
		}
		if allDone {
			s.db.Exec("UPDATE tasks SET status = 'ready', updated_at = ? WHERE id = ?", now, b.id)
		}
	}
	return nil
}

// --- Agents ---

func (s *Store) RegisterAgent(name, agentType, projectID string, role model.AgentRole, parentAgent string) (*model.Agent, error) {
	now := time.Now().UTC()
	if role == "" {
		role = model.AgentRoleWorker
	}
	a := &model.Agent{
		ID:          id.New(),
		Name:        name,
		Type:        agentType,
		Role:        role,
		Status:      model.AgentConnected,
		ProjectID:   projectID,
		ParentAgent: parentAgent,
		ConnectedAt: now,
		LastSeen:    now,
	}

	_, err := s.db.Exec(`INSERT INTO agents (id, name, type, status, current_task, project_id, role, parent_agent, connected_at, last_seen)
		VALUES (?, ?, ?, ?, '', ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET status = 'connected', last_seen = ?, connected_at = ?, role = ?,
		parent_agent = CASE WHEN ? != '' THEN ? ELSE agents.parent_agent END,
		project_id = CASE WHEN ? != '' THEN ? ELSE agents.project_id END`,
		a.ID, a.Name, a.Type, string(a.Status), a.ProjectID, string(role), parentAgent,
		now.Format(time.RFC3339), now.Format(time.RFC3339),
		now.Format(time.RFC3339), now.Format(time.RFC3339), string(role),
		parentAgent, parentAgent,
		projectID, projectID)
	if err != nil {
		return nil, err
	}
	return s.GetAgentByName(name)
}

func (s *Store) GetAgent(id string) (*model.Agent, error) {
	row := s.db.QueryRow(`SELECT id, name, type, status, current_task, project_id, role, parent_agent, connected_at, last_seen FROM agents WHERE id = ?`, id)
	return scanAgent(row)
}

func (s *Store) GetAgentByName(name string) (*model.Agent, error) {
	row := s.db.QueryRow(`SELECT id, name, type, status, current_task, project_id, role, parent_agent, connected_at, last_seen FROM agents WHERE name = ?`, name)
	return scanAgent(row)
}

func (s *Store) ListAgents(statusFilter string) ([]*model.Agent, error) {
	query := `SELECT id, name, type, status, current_task, project_id, role, parent_agent, connected_at, last_seen FROM agents`
	var args []any
	if statusFilter != "" {
		query += " WHERE status = ?"
		args = append(args, statusFilter)
	}
	query += " ORDER BY last_seen DESC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var agents []*model.Agent
	for rows.Next() {
		a, err := scanAgentRows(rows)
		if err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

func (s *Store) UpdateAgentStatus(name string, status model.AgentStatus, currentTask string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec("UPDATE agents SET status = ?, current_task = ?, last_seen = ? WHERE name = ?",
		string(status), currentTask, now, name)
	return err
}

func (s *Store) UpdateAgentProject(name, projectID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec("UPDATE agents SET project_id = ?, last_seen = ? WHERE name = ?", projectID, now, name)
	return err
}

func (s *Store) TouchAgent(name string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec("UPDATE agents SET last_seen = ? WHERE name = ?", now, name)
	return err
}

func (s *Store) DisconnectAgent(name string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec("UPDATE agents SET status = 'disconnected', last_seen = ? WHERE name = ?", now, name)
	if err != nil {
		return err
	}
	// Unassign tasks
	_, err = s.db.Exec("UPDATE tasks SET assignee = '', status = 'ready', updated_at = ? WHERE assignee = ? AND status = 'in_progress'", now, name)
	return err
}

func (s *Store) DeleteAgent(name string) error {
	_, err := s.db.Exec("DELETE FROM agents WHERE name = ?", name)
	return err
}

func (s *Store) PurgeStaleAgents(maxAge time.Duration) (int, error) {
	cutoff := time.Now().UTC().Add(-maxAge).Format(time.RFC3339)
	result, err := s.db.Exec("DELETE FROM agents WHERE status = 'disconnected' AND last_seen < ?", cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}

// --- Events ---

func (s *Store) RecordEvent(e *model.Event) error {
	if e.ID == "" {
		e.ID = id.New()
	}
	e.Timestamp = time.Now().UTC()
	payload, _ := json.Marshal(e.Payload)
	_, err := s.db.Exec(`INSERT INTO events (id, type, agent_id, task_id, payload, timestamp) VALUES (?, ?, ?, ?, ?, ?)`,
		e.ID, string(e.Type), e.AgentID, e.TaskID, string(payload), e.Timestamp.Format(time.RFC3339))
	return err
}

func (s *Store) ListEvents(limit int) ([]*model.Event, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT id, type, agent_id, task_id, payload, timestamp FROM events ORDER BY timestamp DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*model.Event
	for rows.Next() {
		var e model.Event
		var payloadStr, ts string
		if err := rows.Scan(&e.ID, &e.Type, &e.AgentID, &e.TaskID, &payloadStr, &ts); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(payloadStr), &e.Payload)
		e.Timestamp, _ = time.Parse(time.RFC3339, ts)
		events = append(events, &e)
	}
	return events, rows.Err()
}

func (s *Store) ListTaskEvents(taskID string, limit int) ([]*model.Event, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT id, type, agent_id, task_id, payload, timestamp FROM events WHERE task_id = ? ORDER BY timestamp DESC LIMIT ?`, taskID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*model.Event
	for rows.Next() {
		var e model.Event
		var payloadStr, ts string
		if err := rows.Scan(&e.ID, &e.Type, &e.AgentID, &e.TaskID, &payloadStr, &ts); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(payloadStr), &e.Payload)
		e.Timestamp, _ = time.Parse(time.RFC3339, ts)
		events = append(events, &e)
	}
	return events, rows.Err()
}

// ListSubtasks returns all tasks with the given parent_id
func (s *Store) ListSubtasks(parentID string) ([]*model.Task, error) {
	return s.ListTasks(map[string]string{"parent_id": parentID})
}

// SubtaskProgress returns done/total counts for subtasks of a parent
func (s *Store) SubtaskProgress(parentID string) (done, total int, err error) {
	row := s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(CASE WHEN status = 'done' THEN 1 ELSE 0 END), 0) FROM tasks WHERE parent_id = ?`, parentID)
	err = row.Scan(&total, &done)
	return
}

// --- Messages ---

func (s *Store) SendMessage(msg *model.Message) error {
	if msg.ID == "" {
		msg.ID = id.New()
	}
	msg.CreatedAt = time.Now().UTC()
	_, err := s.db.Exec(`INSERT INTO messages (id, "from", "to", body, read, created_at) VALUES (?, ?, ?, ?, 0, ?)`,
		msg.ID, msg.From, msg.To, msg.Body, msg.CreatedAt.Format(time.RFC3339))
	return err
}

func (s *Store) ReadMessages(to string, limit int) ([]*model.Message, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT id, "from", "to", body, read, created_at FROM messages WHERE "to" = ? OR "to" = '' ORDER BY created_at DESC LIMIT ?`, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []*model.Message
	for rows.Next() {
		var m model.Message
		var readInt int
		var ts string
		if err := rows.Scan(&m.ID, &m.From, &m.To, &m.Body, &readInt, &ts); err != nil {
			return nil, err
		}
		m.Read = readInt != 0
		m.CreatedAt, _ = time.Parse(time.RFC3339, ts)
		messages = append(messages, &m)
	}

	// Mark as read
	s.db.Exec(`UPDATE messages SET read = 1 WHERE ("to" = ? OR "to" = '') AND read = 0`, to)
	return messages, rows.Err()
}

func (s *Store) ListAllMessages(limit int) ([]*model.Message, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id, "from", "to", body, read, created_at FROM messages ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []*model.Message
	for rows.Next() {
		var m model.Message
		var readInt int
		var ts string
		if err := rows.Scan(&m.ID, &m.From, &m.To, &m.Body, &readInt, &ts); err != nil {
			return nil, err
		}
		m.Read = readInt != 0
		m.CreatedAt, _ = time.Parse(time.RFC3339, ts)
		messages = append(messages, &m)
	}
	return messages, rows.Err()
}

func (s *Store) MarkMessagesRead(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err := tx.Exec(`UPDATE messages SET read = 1 WHERE id = ?`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) MarkAllMessagesRead() error {
	_, err := s.db.Exec(`UPDATE messages SET read = 1 WHERE read = 0`)
	return err
}

// --- Helpers ---

func (s *Store) checkCycleDeps(taskID string, deps []string) error {
	visited := map[string]bool{taskID: true}
	queue := make([]string, len(deps))
	copy(queue, deps)

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if visited[current] {
			return ErrCycleDep
		}
		visited[current] = true

		var depsJSON string
		err := s.db.QueryRow("SELECT depends_on FROM tasks WHERE id = ?", current).Scan(&depsJSON)
		if err != nil {
			continue
		}
		var transitiveDeps []string
		json.Unmarshal([]byte(depsJSON), &transitiveDeps)
		queue = append(queue, transitiveDeps...)
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanTask(row scanner) (*model.Task, error) {
	var t model.Task
	var criteriaJSON, tagsJSON, dependsOnJSON, deadlineStr, createdStr, updatedStr string
	err := row.Scan(&t.ID, &t.Title, &t.Description, &criteriaJSON,
		&t.Status, &t.Priority, &t.Assignee, &tagsJSON,
		&t.Estimate, &deadlineStr, &createdStr, &updatedStr,
		&t.ParentID, &dependsOnJSON, &t.TaskType, &t.ProjectID,
		&t.IssueNumber, &t.IssueURL)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(criteriaJSON), &t.Criteria)
	json.Unmarshal([]byte(tagsJSON), &t.Tags)
	json.Unmarshal([]byte(dependsOnJSON), &t.DependsOn)
	t.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
	t.UpdatedAt, _ = time.Parse(time.RFC3339, updatedStr)
	if deadlineStr != "" {
		dl, err := time.Parse(time.RFC3339, deadlineStr)
		if err == nil {
			t.Deadline = &dl
		}
	}
	return &t, nil
}

func scanTaskRows(rows *sql.Rows) (*model.Task, error) {
	return scanTask(rows)
}

func scanAgent(row scanner) (*model.Agent, error) {
	var a model.Agent
	var connStr, seenStr string
	err := row.Scan(&a.ID, &a.Name, &a.Type, &a.Status, &a.CurrentTask, &a.ProjectID, &a.Role, &a.ParentAgent, &connStr, &seenStr)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.ConnectedAt, _ = time.Parse(time.RFC3339, connStr)
	a.LastSeen, _ = time.Parse(time.RFC3339, seenStr)
	if a.Role == "" {
		a.Role = model.AgentRoleWorker
	}
	return &a, nil
}

func scanAgentRows(rows *sql.Rows) (*model.Agent, error) {
	return scanAgent(rows)
}

// TaskDeps returns dependency info: what this task depends on and what depends on it
func (s *Store) TaskDeps(taskID string) (dependsOn []*model.Task, blockedBy []*model.Task, err error) {
	// Get the task's own dependencies
	task, err := s.GetTask(taskID)
	if err != nil {
		return nil, nil, err
	}

	// Fetch tasks this one depends on
	for _, depID := range task.DependsOn {
		if dep, err := s.GetTask(depID); err == nil {
			dependsOn = append(dependsOn, dep)
		}
	}

	// Find tasks that depend on this one
	rows, err := s.db.Query("SELECT id, depends_on FROM tasks")
	if err != nil {
		return dependsOn, nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var id, depsJSON string
		rows.Scan(&id, &depsJSON)
		var deps []string
		json.Unmarshal([]byte(depsJSON), &deps)
		for _, d := range deps {
			if d == taskID {
				if t, err := s.GetTask(id); err == nil {
					blockedBy = append(blockedBy, t)
				}
				break
			}
		}
	}

	return dependsOn, blockedBy, nil
}

// --- Comments ---

func (s *Store) AddComment(c *model.Comment) error {
	if c.ID == "" {
		c.ID = id.New()
	}
	c.CreatedAt = time.Now().UTC()
	_, err := s.db.Exec(`INSERT INTO comments (id, task_id, author, body, created_at) VALUES (?, ?, ?, ?, ?)`,
		c.ID, c.TaskID, c.Author, c.Body, c.CreatedAt.Format(time.RFC3339))
	return err
}

func (s *Store) ListComments(taskID string) ([]*model.Comment, error) {
	rows, err := s.db.Query(`SELECT id, task_id, author, body, created_at FROM comments WHERE task_id = ? ORDER BY created_at ASC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var comments []*model.Comment
	for rows.Next() {
		var c model.Comment
		var ts string
		if err := rows.Scan(&c.ID, &c.TaskID, &c.Author, &c.Body, &ts); err != nil {
			return nil, err
		}
		c.CreatedAt, _ = time.Parse(time.RFC3339, ts)
		comments = append(comments, &c)
	}
	return comments, nil
}

// --- Projects ---

func (s *Store) CreateProject(p *model.Project) error {
	if p.ID == "" {
		p.ID = id.New()
	}
	now := time.Now().UTC()
	p.CreatedAt = now
	p.UpdatedAt = now
	_, err := s.db.Exec(`INSERT INTO projects (id, name, description, leader_agent, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Description, p.LeaderAgent, p.CreatedAt.Format(time.RFC3339), p.UpdatedAt.Format(time.RFC3339))
	return err
}

func (s *Store) GetProject(id string) (*model.Project, error) {
	var p model.Project
	var createdStr, updatedStr string
	err := s.db.QueryRow(`SELECT id, name, description, leader_agent, created_at, updated_at FROM projects WHERE id = ?`, id).
		Scan(&p.ID, &p.Name, &p.Description, &p.LeaderAgent, &createdStr, &updatedStr)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
	p.UpdatedAt, _ = time.Parse(time.RFC3339, updatedStr)
	return &p, nil
}

func (s *Store) ListProjects() ([]*model.Project, error) {
	rows, err := s.db.Query(`SELECT id, name, description, leader_agent, created_at, updated_at FROM projects ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var projects []*model.Project
	for rows.Next() {
		var p model.Project
		var createdStr, updatedStr string
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.LeaderAgent, &createdStr, &updatedStr); err != nil {
			return nil, err
		}
		p.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		p.UpdatedAt, _ = time.Parse(time.RFC3339, updatedStr)
		projects = append(projects, &p)
	}
	return projects, rows.Err()
}

func (s *Store) UpdateProject(id string, updates map[string]any) (*model.Project, error) {
	_, err := s.GetProject(id)
	if err != nil {
		return nil, err
	}
	var sets []string
	var args []any
	for k, v := range updates {
		switch k {
		case "name":
			sets = append(sets, "name = ?")
			args = append(args, v)
		case "description":
			sets = append(sets, "description = ?")
			args = append(args, v)
		case "leader_agent":
			sets = append(sets, "leader_agent = ?")
			args = append(args, v)
		}
	}
	if len(sets) == 0 {
		return s.GetProject(id)
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, time.Now().UTC().Format(time.RFC3339))
	args = append(args, id)
	_, err = s.db.Exec("UPDATE projects SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...)
	if err != nil {
		return nil, err
	}
	return s.GetProject(id)
}

func (s *Store) DeleteProject(id string) error {
	_, err := s.GetProject(id)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("DELETE FROM projects WHERE id = ?", id)
	return err
}

// --- Reviews ---

func (s *Store) CreateReview(r *model.Review) error {
	if r.ID == "" {
		r.ID = id.New()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	r.CreatedAt, _ = time.Parse(time.RFC3339, now)
	r.UpdatedAt = r.CreatedAt
	if r.Status == "" {
		r.Status = model.ReviewPending
	}
	_, err := s.db.Exec(`INSERT INTO reviews (id, task_id, agent_id, branch, diff, summary, status, feedback, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.TaskID, r.AgentID, r.Branch, r.Diff, r.Summary, r.Status, r.Feedback, now, now)
	return err
}

func (s *Store) GetReview(id string) (*model.Review, error) {
	row := s.db.QueryRow(`SELECT id, task_id, agent_id, branch, diff, summary, status, feedback, created_at, updated_at FROM reviews WHERE id = ?`, id)
	return scanReview(row)
}

func (s *Store) ListReviewsByTask(taskID string) ([]*model.Review, error) {
	rows, err := s.db.Query(`SELECT id, task_id, agent_id, branch, diff, summary, status, feedback, created_at, updated_at FROM reviews WHERE task_id = ? ORDER BY created_at DESC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var reviews []*model.Review
	for rows.Next() {
		r, err := scanReview(rows)
		if err != nil {
			return nil, err
		}
		reviews = append(reviews, r)
	}
	return reviews, nil
}

func (s *Store) ListReviews(statusFilter string) ([]*model.Review, error) {
	query := `SELECT id, task_id, agent_id, branch, diff, summary, status, feedback, created_at, updated_at FROM reviews`
	var args []any
	if statusFilter != "" {
		query += ` WHERE status = ?`
		args = append(args, statusFilter)
	}
	query += ` ORDER BY created_at DESC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var reviews []*model.Review
	for rows.Next() {
		r, err := scanReview(rows)
		if err != nil {
			return nil, err
		}
		reviews = append(reviews, r)
	}
	return reviews, nil
}

func (s *Store) UpdateReviewStatus(id string, status model.ReviewStatus, feedback string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`UPDATE reviews SET status = ?, feedback = ?, updated_at = ? WHERE id = ?`, status, feedback, now, id)
	return err
}

func scanReview(s scanner) (*model.Review, error) {
	var r model.Review
	var createdAt, updatedAt string
	err := s.Scan(&r.ID, &r.TaskID, &r.AgentID, &r.Branch, &r.Diff, &r.Summary, &r.Status, &r.Feedback, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	r.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	r.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return &r, nil
}

// --- Token Usage ---

func (s *Store) RecordTokenUsage(u *model.TokenUsage) error {
	if u.ID == "" {
		u.ID = id.New()
	}
	u.CreatedAt = time.Now().UTC()
	u.TotalTokens = u.InputTokens + u.OutputTokens
	if u.CostUSD == 0 {
		u.CostUSD = model.CalculateCost(u.Model, u.InputTokens, u.OutputTokens)
	}
	_, err := s.db.Exec(`INSERT INTO token_usage (id, agent_name, model, input_tokens, output_tokens, total_tokens, cost_usd, task_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.AgentName, u.Model, u.InputTokens, u.OutputTokens, u.TotalTokens, u.CostUSD, u.TaskID,
		u.CreatedAt.Format(time.RFC3339))
	return err
}

func (s *Store) TokenUsageByAgent() ([]*model.TokenSummary, error) {
	rows, err := s.db.Query(`SELECT agent_name, COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(total_tokens),0), COALESCE(SUM(cost_usd),0), COUNT(*) FROM token_usage GROUP BY agent_name ORDER BY SUM(cost_usd) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var summaries []*model.TokenSummary
	for rows.Next() {
		var ts model.TokenSummary
		if err := rows.Scan(&ts.AgentName, &ts.InputTokens, &ts.OutputTokens, &ts.TotalTokens, &ts.CostUSD, &ts.Reports); err != nil {
			return nil, err
		}
		summaries = append(summaries, &ts)
	}
	return summaries, rows.Err()
}

func (s *Store) TokenUsageTotal() (*model.TokenSummary, error) {
	var ts model.TokenSummary
	err := s.db.QueryRow(`SELECT COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(total_tokens),0), COALESCE(SUM(cost_usd),0), COUNT(*) FROM token_usage`).
		Scan(&ts.InputTokens, &ts.OutputTokens, &ts.TotalTokens, &ts.CostUSD, &ts.Reports)
	if err != nil {
		return nil, err
	}
	return &ts, nil
}

func (s *Store) TokenUsageRecent(limit int) ([]*model.TokenUsage, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT id, agent_name, model, input_tokens, output_tokens, total_tokens, cost_usd, task_id, created_at FROM token_usage ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var usages []*model.TokenUsage
	for rows.Next() {
		var u model.TokenUsage
		var ts string
		if err := rows.Scan(&u.ID, &u.AgentName, &u.Model, &u.InputTokens, &u.OutputTokens, &u.TotalTokens, &u.CostUSD, &u.TaskID, &ts); err != nil {
			return nil, err
		}
		u.CreatedAt, _ = time.Parse(time.RFC3339, ts)
		usages = append(usages, &u)
	}
	return usages, rows.Err()
}

// --- Stats ---

type Stats struct {
	TasksByStatus   map[string]int         `json:"tasks_by_status"`
	TasksByPriority map[string]int         `json:"tasks_by_priority"`
	TotalTasks      int                    `json:"total_tasks"`
	AgentsByStatus  map[string]int         `json:"agents_by_status"`
	TotalAgents     int                    `json:"total_agents"`
	UnreadMessages  int                    `json:"unread_messages"`
	TokenUsage      *model.TokenSummary    `json:"token_usage,omitempty"`
	TokenByAgent    []*model.TokenSummary  `json:"token_by_agent,omitempty"`
}

func (s *Store) Stats() (*Stats, error) {
	stats := &Stats{
		TasksByStatus:   map[string]int{},
		TasksByPriority: map[string]int{},
		AgentsByStatus:  map[string]int{},
	}

	// Tasks by status
	rows, err := s.db.Query("SELECT status, COUNT(*) FROM tasks GROUP BY status")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var status string
		var count int
		rows.Scan(&status, &count)
		stats.TasksByStatus[status] = count
		stats.TotalTasks += count
	}
	rows.Close()

	// Tasks by priority
	rows, err = s.db.Query("SELECT priority, COUNT(*) FROM tasks GROUP BY priority")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var priority string
		var count int
		rows.Scan(&priority, &count)
		stats.TasksByPriority[priority] = count
	}
	rows.Close()

	// Agents by status
	rows, err = s.db.Query("SELECT status, COUNT(*) FROM agents GROUP BY status")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var status string
		var count int
		rows.Scan(&status, &count)
		stats.AgentsByStatus[status] = count
		stats.TotalAgents += count
	}
	rows.Close()

	// Unread messages
	s.db.QueryRow("SELECT COUNT(*) FROM messages WHERE read = 0").Scan(&stats.UnreadMessages)

	// Token usage
	if total, err := s.TokenUsageTotal(); err == nil {
		stats.TokenUsage = total
	}
	if byAgent, err := s.TokenUsageByAgent(); err == nil {
		stats.TokenByAgent = byAgent
	}

	return stats, nil
}

// --- Push Subscriptions ---

type PushSubscription struct {
	ID       string `json:"id"`
	Endpoint string `json:"endpoint"`
	Auth     string `json:"auth"`
	P256dh   string `json:"p256dh"`
}

func (s *Store) SavePushSubscription(sub *PushSubscription) error {
	if sub.ID == "" {
		sub.ID = id.New()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO push_subscriptions (id, endpoint, auth, p256dh, created_at) VALUES (?, ?, ?, ?, ?)`,
		sub.ID, sub.Endpoint, sub.Auth, sub.P256dh, now,
	)
	return err
}

func (s *Store) ListPushSubscriptions() ([]*PushSubscription, error) {
	rows, err := s.db.Query(`SELECT id, endpoint, auth, p256dh FROM push_subscriptions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var subs []*PushSubscription
	for rows.Next() {
		var sub PushSubscription
		if err := rows.Scan(&sub.ID, &sub.Endpoint, &sub.Auth, &sub.P256dh); err != nil {
			return nil, err
		}
		subs = append(subs, &sub)
	}
	return subs, rows.Err()
}

func (s *Store) DeletePushSubscription(endpoint string) error {
	_, err := s.db.Exec(`DELETE FROM push_subscriptions WHERE endpoint = ?`, endpoint)
	return err
}
