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
	ErrInvalidStatus  = errors.New("invalid status")
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
	// SQLite supports one writer at a time; limit connections to avoid SQLITE_BUSY
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
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

func (s *Store) Exec(query string, args ...any) (sql.Result, error) {
	return s.db.Exec(query, args...)
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
			project_id TEXT DEFAULT '',
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

		CREATE TABLE IF NOT EXISTS settings (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);

		CREATE INDEX IF NOT EXISTS idx_comments_task ON comments(task_id);
		CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
		CREATE INDEX IF NOT EXISTS idx_tasks_assignee ON tasks(assignee);
		CREATE INDEX IF NOT EXISTS idx_agents_name ON agents(name);
		CREATE INDEX IF NOT EXISTS idx_events_timestamp ON events(timestamp);
		CREATE INDEX IF NOT EXISTS idx_messages_to ON messages("to");
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
		{"agents", "persona_id", "TEXT DEFAULT ''"},
		{"projects", "auto_dispatch", "INTEGER DEFAULT 0"},
		{"projects", "work_dir", "TEXT DEFAULT ''"},
		{"messages", "project_id", "TEXT DEFAULT ''"},
		{"projects", "status", "TEXT DEFAULT 'active'"},
		{"projects", "account", "TEXT DEFAULT ''"},
		{"projects", "category", "TEXT DEFAULT ''"},
		{"projects", "last_touched_at", "TEXT DEFAULT ''"},
		{"projects", "parking_note", "TEXT DEFAULT ''"},
		{"projects", "health", "TEXT DEFAULT 'unknown'"},
		{"projects", "revenue_status", "TEXT DEFAULT ''"},
		{"projects", "tech_stack", "TEXT DEFAULT ''"},
		{"tasks", "sprint_id", "TEXT DEFAULT ''"},
		{"tasks", "story_points", "INTEGER DEFAULT 0"},
		{"tasks", "board_order", "REAL DEFAULT 0"},
	} {
		var count int
		s.db.QueryRow("SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?", col.table, col.name).Scan(&count)
		if count == 0 {
			s.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", col.table, col.name, col.def))
		}
	}

	// Context manager tables
	s.db.Exec(`CREATE TABLE IF NOT EXISTS sessions (
		id         TEXT PRIMARY KEY,
		project_id TEXT NOT NULL,
		started_at TEXT NOT NULL,
		ended_at   TEXT DEFAULT '',
		summary    TEXT DEFAULT '',
		account    TEXT DEFAULT '',
		FOREIGN KEY (project_id) REFERENCES projects(id)
	)`)
	s.db.Exec(`CREATE TABLE IF NOT EXISTS progress (
		id         TEXT PRIMARY KEY,
		project_id TEXT NOT NULL,
		source     TEXT NOT NULL,
		summary    TEXT NOT NULL,
		detail     TEXT DEFAULT '',
		created_at TEXT NOT NULL,
		FOREIGN KEY (project_id) REFERENCES projects(id)
	)`)

	// Create indexes that depend on migrated columns (must run after ALTER TABLE)
	s.db.Exec("CREATE INDEX IF NOT EXISTS idx_tasks_project ON tasks(project_id)")
	s.db.Exec("CREATE INDEX IF NOT EXISTS idx_messages_project ON messages(project_id)")
	s.db.Exec("CREATE INDEX IF NOT EXISTS idx_sessions_project ON sessions(project_id)")
	s.db.Exec("CREATE INDEX IF NOT EXISTS idx_progress_project ON progress(project_id)")
	s.db.Exec("CREATE INDEX IF NOT EXISTS idx_progress_created ON progress(created_at)")
	s.db.Exec("CREATE INDEX IF NOT EXISTS idx_projects_status ON projects(status)")
	s.db.Exec("CREATE INDEX IF NOT EXISTS idx_projects_health ON projects(health)")

	s.db.Exec(`CREATE TABLE IF NOT EXISTS revenue (
		id         TEXT PRIMARY KEY,
		project_id TEXT NOT NULL,
		amount     REAL NOT NULL,
		source     TEXT NOT NULL,
		note       TEXT DEFAULT '',
		date       TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY (project_id) REFERENCES projects(id)
	)`)
	s.db.Exec("CREATE INDEX IF NOT EXISTS idx_revenue_project ON revenue(project_id)")
	s.db.Exec("CREATE INDEX IF NOT EXISTS idx_revenue_date ON revenue(date)")

	s.db.Exec(`CREATE TABLE IF NOT EXISTS sprints (
		id         TEXT PRIMARY KEY,
		project_id TEXT NOT NULL,
		name       TEXT NOT NULL,
		goal       TEXT DEFAULT '',
		state      TEXT DEFAULT 'planned',
		start_date TEXT DEFAULT '',
		end_date   TEXT DEFAULT '',
		created_at TEXT NOT NULL
	)`)
	s.db.Exec("CREATE INDEX IF NOT EXISTS idx_sprints_project ON sprints(project_id)")
	s.db.Exec("CREATE INDEX IF NOT EXISTS idx_sprints_state ON sprints(state)")
	s.db.Exec("CREATE INDEX IF NOT EXISTS idx_tasks_sprint ON tasks(sprint_id)")

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

	_, err := s.db.Exec(`INSERT INTO tasks (id, title, description, criteria, status, priority, assignee, tags, estimate, deadline, created_at, updated_at, parent_id, depends_on, task_type, project_id, issue_number, issue_url, sprint_id, story_points, board_order)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Title, t.Description, string(criteria), string(t.Status), string(t.Priority),
		t.Assignee, string(tags), t.Estimate, deadline,
		t.CreatedAt.Format(time.RFC3339), t.UpdatedAt.Format(time.RFC3339),
		t.ParentID, string(dependsOn), string(t.TaskType), t.ProjectID,
		t.IssueNumber, t.IssueURL, t.SprintID, t.StoryPoints, t.BoardOrder)
	return err
}

func (s *Store) GetTask(id string) (*model.Task, error) {
	row := s.db.QueryRow(`SELECT id, title, description, criteria, status, priority, assignee, tags, estimate, deadline, created_at, updated_at, parent_id, depends_on, task_type, project_id, issue_number, issue_url, sprint_id, story_points, board_order FROM tasks WHERE id = ?`, id)
	return scanTask(row)
}

func (s *Store) ListTasks(filters map[string]string) ([]*model.Task, error) {
	query := `SELECT id, title, description, criteria, status, priority, assignee, tags, estimate, deadline, created_at, updated_at, parent_id, depends_on, task_type, project_id, issue_number, issue_url, sprint_id, story_points, board_order FROM tasks`
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
	if v, ok := filters["sprint_id"]; ok {
		if v == "__backlog__" {
			conditions = append(conditions, "COALESCE(sprint_id,'') = ''")
		} else {
			conditions = append(conditions, "sprint_id = ?")
			args = append(args, v)
		}
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

func (s *Store) MoveTask(taskID, status string, boardOrder float64) (*model.Task, error) {
	if !model.TaskStatus(status).Valid() {
		return nil, ErrInvalidStatus
	}
	if _, err := s.GetTask(taskID); err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec("UPDATE tasks SET status = ?, board_order = ?, updated_at = ? WHERE id = ?",
		status, boardOrder, now, taskID)
	if err != nil {
		return nil, err
	}
	return s.GetTask(taskID)
}

func (s *Store) AssignToSprint(taskID, sprintID string) error {
	if _, err := s.GetTask(taskID); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec("UPDATE tasks SET sprint_id = ?, updated_at = ? WHERE id = ?", sprintID, now, taskID)
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
	row := s.db.QueryRow(`SELECT id, name, type, status, current_task, project_id, role, parent_agent, persona_id, connected_at, last_seen FROM agents WHERE id = ?`, id)
	return scanAgent(row)
}

func (s *Store) GetAgentByName(name string) (*model.Agent, error) {
	row := s.db.QueryRow(`SELECT id, name, type, status, current_task, project_id, role, parent_agent, persona_id, connected_at, last_seen FROM agents WHERE name = ?`, name)
	return scanAgent(row)
}

func (s *Store) ListAgents(statusFilter string) ([]*model.Agent, error) {
	query := `SELECT id, name, type, status, current_task, project_id, role, parent_agent, persona_id, connected_at, last_seen FROM agents`
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
	result, err := s.db.Exec("DELETE FROM agents WHERE name = ?", name)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
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

func (s *Store) ListEventsSince(afterID string, limit int) ([]*model.Event, error) {
	if limit <= 0 {
		limit = 200
	}
	// Use rowid to get events inserted after the given event ID
	rows, err := s.db.Query(
		`SELECT id, type, agent_id, task_id, payload, timestamp FROM events WHERE rowid > (SELECT rowid FROM events WHERE id = ?) ORDER BY rowid ASC LIMIT ?`,
		afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*model.Event
	for rows.Next() {
		var e model.Event
		var payloadStr, ets string
		if err := rows.Scan(&e.ID, &e.Type, &e.AgentID, &e.TaskID, &payloadStr, &ets); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(payloadStr), &e.Payload)
		e.Timestamp, _ = time.Parse(time.RFC3339, ets)
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
	_, err := s.db.Exec(`INSERT INTO messages (id, "from", "to", body, read, project_id, created_at) VALUES (?, ?, ?, ?, 0, ?, ?)`,
		msg.ID, msg.From, msg.To, msg.Body, msg.ProjectID, msg.CreatedAt.Format(time.RFC3339))
	return err
}

func (s *Store) ReadMessages(to string, limit int) ([]*model.Message, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT id, "from", "to", body, read, project_id, created_at FROM messages WHERE "to" = ? OR "to" = '' ORDER BY created_at DESC LIMIT ?`, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []*model.Message
	for rows.Next() {
		var m model.Message
		var readInt int
		var ts string
		if err := rows.Scan(&m.ID, &m.From, &m.To, &m.Body, &readInt, &m.ProjectID, &ts); err != nil {
			return nil, err
		}
		m.Read = readInt != 0
		m.CreatedAt, _ = time.Parse(time.RFC3339, ts)
		messages = append(messages, &m)
	}

	return messages, rows.Err()
}

func (s *Store) ListAllMessages(limit int) ([]*model.Message, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id, "from", "to", body, read, project_id, created_at FROM messages ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []*model.Message
	for rows.Next() {
		var m model.Message
		var readInt int
		var ts string
		if err := rows.Scan(&m.ID, &m.From, &m.To, &m.Body, &readInt, &m.ProjectID, &ts); err != nil {
			return nil, err
		}
		m.Read = readInt != 0
		m.CreatedAt, _ = time.Parse(time.RFC3339, ts)
		messages = append(messages, &m)
	}
	return messages, rows.Err()
}

func (s *Store) AgentMessages(agent string, limit int) ([]*model.Message, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(
		`SELECT id, "from", "to", body, read, project_id, created_at FROM messages WHERE "from" = ? OR "to" = ? ORDER BY created_at DESC LIMIT ?`,
		agent, agent, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []*model.Message
	for rows.Next() {
		var m model.Message
		var readInt int
		var ts string
		if err := rows.Scan(&m.ID, &m.From, &m.To, &m.Body, &readInt, &m.ProjectID, &ts); err != nil {
			return nil, err
		}
		m.Read = readInt != 0
		m.CreatedAt, _ = time.Parse(time.RFC3339, ts)
		messages = append(messages, &m)
	}
	return messages, rows.Err()
}

func (s *Store) ProjectMessages(projectID string, limit int) ([]*model.Message, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(
		`SELECT id, "from", "to", body, read, project_id, created_at FROM messages WHERE project_id = ? ORDER BY created_at ASC LIMIT ?`,
		projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []*model.Message
	for rows.Next() {
		var m model.Message
		var readInt int
		var ts string
		if err := rows.Scan(&m.ID, &m.From, &m.To, &m.Body, &readInt, &m.ProjectID, &ts); err != nil {
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

func (s *Store) SearchMessages(query string, limit int) ([]*model.Message, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(
		`SELECT id, "from", "to", body, read, project_id, created_at FROM messages WHERE body LIKE ? ORDER BY created_at DESC LIMIT ?`,
		"%"+query+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []*model.Message
	for rows.Next() {
		var m model.Message
		var readInt int
		var ts string
		if err := rows.Scan(&m.ID, &m.From, &m.To, &m.Body, &readInt, &m.ProjectID, &ts); err != nil {
			return nil, err
		}
		m.Read = readInt != 0
		m.CreatedAt, _ = time.Parse(time.RFC3339, ts)
		messages = append(messages, &m)
	}
	return messages, rows.Err()
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
		&t.IssueNumber, &t.IssueURL, &t.SprintID, &t.StoryPoints, &t.BoardOrder)
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
	err := row.Scan(&a.ID, &a.Name, &a.Type, &a.Status, &a.CurrentTask, &a.ProjectID, &a.Role, &a.ParentAgent, &a.PersonaID, &connStr, &seenStr)
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
		if err := rows.Scan(&id, &depsJSON); err != nil {
			continue
		}
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
	if p.Status == "" {
		p.Status = model.ProjectActive
	}
	if p.Health == "" {
		p.Health = model.HealthUnknown
	}
	autoDispatch := 0
	if p.AutoDispatch {
		autoDispatch = 1
	}
	lastTouched := ""
	if p.LastTouchedAt != nil {
		lastTouched = p.LastTouchedAt.Format(time.RFC3339)
	}
	_, err := s.db.Exec(`INSERT INTO projects (id, name, description, leader_agent, auto_dispatch, work_dir, status, account, category, last_touched_at, parking_note, health, revenue_status, tech_stack, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Description, p.LeaderAgent, autoDispatch, p.WorkDir, string(p.Status), p.Account, p.Category, lastTouched, p.ParkingNote, string(p.Health), p.RevenueStatus, p.TechStack, p.CreatedAt.Format(time.RFC3339), p.UpdatedAt.Format(time.RFC3339))
	return err
}

func (s *Store) GetProject(projID string) (*model.Project, error) {
	var p model.Project
	var createdStr, updatedStr, lastTouchedStr string
	var autoDispatch int
	var status, health string
	err := s.db.QueryRow(`SELECT id, name, description, leader_agent, auto_dispatch, work_dir, COALESCE(status,'active'), COALESCE(account,''), COALESCE(category,''), COALESCE(last_touched_at,''), COALESCE(parking_note,''), COALESCE(health,'unknown'), COALESCE(revenue_status,''), COALESCE(tech_stack,''), created_at, updated_at FROM projects WHERE id = ?`, projID).
		Scan(&p.ID, &p.Name, &p.Description, &p.LeaderAgent, &autoDispatch, &p.WorkDir, &status, &p.Account, &p.Category, &lastTouchedStr, &p.ParkingNote, &health, &p.RevenueStatus, &p.TechStack, &createdStr, &updatedStr)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.AutoDispatch = autoDispatch != 0
	p.Status = model.ProjectStatus(status)
	p.Health = model.ProjectHealth(health)
	p.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
	p.UpdatedAt, _ = time.Parse(time.RFC3339, updatedStr)
	if lastTouchedStr != "" {
		t, _ := time.Parse(time.RFC3339, lastTouchedStr)
		p.LastTouchedAt = &t
	}
	return &p, nil
}

func (s *Store) ListProjects() ([]*model.Project, error) {
	rows, err := s.db.Query(`SELECT id, name, description, leader_agent, auto_dispatch, work_dir, COALESCE(status,'active'), COALESCE(account,''), COALESCE(category,''), COALESCE(last_touched_at,''), COALESCE(parking_note,''), COALESCE(health,'unknown'), COALESCE(revenue_status,''), COALESCE(tech_stack,''), created_at, updated_at FROM projects ORDER BY last_touched_at DESC, created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var projects []*model.Project
	for rows.Next() {
		var p model.Project
		var createdStr, updatedStr, lastTouchedStr string
		var autoDispatch int
		var status, health string
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.LeaderAgent, &autoDispatch, &p.WorkDir, &status, &p.Account, &p.Category, &lastTouchedStr, &p.ParkingNote, &health, &p.RevenueStatus, &p.TechStack, &createdStr, &updatedStr); err != nil {
			return nil, err
		}
		p.AutoDispatch = autoDispatch != 0
		p.Status = model.ProjectStatus(status)
		p.Health = model.ProjectHealth(health)
		p.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		p.UpdatedAt, _ = time.Parse(time.RFC3339, updatedStr)
		if lastTouchedStr != "" {
			t, _ := time.Parse(time.RFC3339, lastTouchedStr)
			p.LastTouchedAt = &t
		}
		projects = append(projects, &p)
	}
	return projects, rows.Err()
}

func (s *Store) UpdateProject(projID string, updates map[string]any) (*model.Project, error) {
	_, err := s.GetProject(projID)
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
		case "auto_dispatch":
			val := 0
			switch b := v.(type) {
			case bool:
				if b {
					val = 1
				}
			case float64:
				if b != 0 {
					val = 1
				}
			}
			sets = append(sets, "auto_dispatch = ?")
			args = append(args, val)
		case "work_dir":
			sets = append(sets, "work_dir = ?")
			args = append(args, v)
		case "status":
			sets = append(sets, "status = ?")
			args = append(args, v)
		case "account":
			sets = append(sets, "account = ?")
			args = append(args, v)
		case "category":
			sets = append(sets, "category = ?")
			args = append(args, v)
		case "parking_note":
			sets = append(sets, "parking_note = ?")
			args = append(args, v)
		case "health":
			sets = append(sets, "health = ?")
			args = append(args, v)
		case "revenue_status":
			sets = append(sets, "revenue_status = ?")
			args = append(args, v)
		case "tech_stack":
			sets = append(sets, "tech_stack = ?")
			args = append(args, v)
		case "last_touched_at":
			sets = append(sets, "last_touched_at = ?")
			args = append(args, v)
		}
	}
	if len(sets) == 0 {
		return s.GetProject(projID)
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, time.Now().UTC().Format(time.RFC3339))
	args = append(args, projID)
	_, err = s.db.Exec("UPDATE projects SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...)
	if err != nil {
		return nil, err
	}
	return s.GetProject(projID)
}

func (s *Store) DeleteProject(id string) error {
	_, err := s.GetProject(id)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("DELETE FROM projects WHERE id = ?", id)
	return err
}

// --- Sessions ---

func (s *Store) CreateSession(sess *model.Session) error {
	if sess.ID == "" {
		sess.ID = id.New()
	}
	sess.StartedAt = time.Now().UTC()
	_, err := s.db.Exec(`INSERT INTO sessions (id, project_id, started_at, ended_at, summary, account) VALUES (?, ?, ?, '', '', ?)`,
		sess.ID, sess.ProjectID, sess.StartedAt.Format(time.RFC3339), sess.Account)
	if err != nil {
		return err
	}
	// Touch the project
	s.db.Exec("UPDATE projects SET last_touched_at = ? WHERE id = ?", sess.StartedAt.Format(time.RFC3339), sess.ProjectID)
	return nil
}

func (s *Store) EndSession(sessID, summary string) error {
	now := time.Now().UTC()
	_, err := s.db.Exec(`UPDATE sessions SET ended_at = ?, summary = ? WHERE id = ?`,
		now.Format(time.RFC3339), summary, sessID)
	return err
}

func (s *Store) ListSessions(projectID string, limit int) ([]*model.Session, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`SELECT id, project_id, started_at, ended_at, summary, account FROM sessions WHERE project_id = ? ORDER BY started_at DESC LIMIT ?`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []*model.Session
	for rows.Next() {
		var sess model.Session
		var startedStr, endedStr string
		if err := rows.Scan(&sess.ID, &sess.ProjectID, &startedStr, &endedStr, &sess.Summary, &sess.Account); err != nil {
			return nil, err
		}
		sess.StartedAt, _ = time.Parse(time.RFC3339, startedStr)
		if endedStr != "" {
			t, _ := time.Parse(time.RFC3339, endedStr)
			sess.EndedAt = &t
		}
		sessions = append(sessions, &sess)
	}
	return sessions, rows.Err()
}

// --- Progress ---

func (s *Store) CreateProgress(p *model.Progress) error {
	if p.ID == "" {
		p.ID = id.New()
	}
	p.CreatedAt = time.Now().UTC()
	_, err := s.db.Exec(`INSERT INTO progress (id, project_id, source, summary, detail, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		p.ID, p.ProjectID, p.Source, p.Summary, p.Detail, p.CreatedAt.Format(time.RFC3339))
	if err != nil {
		return err
	}
	// Touch the project
	s.db.Exec("UPDATE projects SET last_touched_at = ? WHERE id = ?", p.CreatedAt.Format(time.RFC3339), p.ProjectID)
	return nil
}

func (s *Store) ListProgress(projectID string, limit int) ([]*model.Progress, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`SELECT id, project_id, source, summary, detail, created_at FROM progress WHERE project_id = ? ORDER BY created_at DESC LIMIT ?`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []*model.Progress
	for rows.Next() {
		var p model.Progress
		var createdStr string
		if err := rows.Scan(&p.ID, &p.ProjectID, &p.Source, &p.Summary, &p.Detail, &createdStr); err != nil {
			return nil, err
		}
		p.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		entries = append(entries, &p)
	}
	return entries, rows.Err()
}

func (s *Store) ListAllProgress(limit int) ([]*model.Progress, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT id, project_id, source, summary, detail, created_at FROM progress ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []*model.Progress
	for rows.Next() {
		var p model.Progress
		var createdStr string
		if err := rows.Scan(&p.ID, &p.ProjectID, &p.Source, &p.Summary, &p.Detail, &createdStr); err != nil {
			return nil, err
		}
		p.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		entries = append(entries, &p)
	}
	return entries, rows.Err()
}

// TaskCountByProject returns count of open tasks (not done/killed) per project.
func (s *Store) TaskCountByProject(projectID string) (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE project_id = ? AND status NOT IN ('done')`, projectID).Scan(&count)
	return count, err
}

// --- Stats ---

type DayCount struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

type Stats struct {
	TasksByStatus   map[string]int `json:"tasks_by_status"`
	TasksByPriority map[string]int `json:"tasks_by_priority"`
	TotalTasks      int            `json:"total_tasks"`
	AgentsByStatus  map[string]int `json:"agents_by_status"`
	TotalAgents     int            `json:"total_agents"`
	UnreadMessages  int            `json:"unread_messages"`
	Velocity        []DayCount     `json:"velocity,omitempty"`
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

	// Velocity: tasks completed per day over the past 7 days
	sevenDaysAgo := time.Now().UTC().Add(-7 * 24 * time.Hour).Format("2006-01-02")
	rows, err = s.db.Query(
		`SELECT DATE(updated_at) as day, COUNT(*) FROM tasks WHERE status = 'done' AND DATE(updated_at) >= ? GROUP BY day ORDER BY day`,
		sevenDaysAgo)
	if err == nil {
		for rows.Next() {
			var day string
			var count int
			rows.Scan(&day, &count)
			stats.Velocity = append(stats.Velocity, DayCount{Date: day, Count: count})
		}
		rows.Close()
	}

	return stats, nil
}

// --- Revenue ---

func (s *Store) CreateRevenue(r *model.Revenue) error {
	if r.ID == "" {
		r.ID = id.New()
	}
	r.CreatedAt = time.Now().UTC()
	_, err := s.db.Exec(
		`INSERT INTO revenue (id, project_id, amount, source, note, date, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.ProjectID, r.Amount, r.Source, r.Note, r.Date, r.CreatedAt.Format(time.RFC3339),
	)
	return err
}

func (s *Store) ListRevenue(projectID string) ([]*model.Revenue, error) {
	query := `SELECT id, project_id, amount, source, note, date, created_at FROM revenue`
	args := []any{}
	if projectID != "" {
		query += ` WHERE project_id = ?`
		args = append(args, projectID)
	}
	query += ` ORDER BY date DESC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []*model.Revenue
	for rows.Next() {
		var r model.Revenue
		var createdAt string
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.Amount, &r.Source, &r.Note, &r.Date, &createdAt); err != nil {
			return nil, err
		}
		r.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		results = append(results, &r)
	}
	return results, rows.Err()
}

func (s *Store) RevenueSummary() (map[string]float64, float64, error) {
	rows, err := s.db.Query(`SELECT project_id, SUM(amount) FROM revenue GROUP BY project_id`)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	byProject := map[string]float64{}
	var total float64
	for rows.Next() {
		var pid string
		var sum float64
		rows.Scan(&pid, &sum)
		byProject[pid] = sum
		total += sum
	}
	return byProject, total, rows.Err()
}

// --- Sprints ---

func (s *Store) CreateSprint(sp *model.Sprint) error {
	if sp.ID == "" {
		sp.ID = id.New()
	}
	if sp.CreatedAt.IsZero() {
		sp.CreatedAt = time.Now().UTC()
	}
	if sp.State == "" {
		sp.State = model.SprintPlanned
	}
	_, err := s.db.Exec(`INSERT INTO sprints (id, project_id, name, goal, state, start_date, end_date, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		sp.ID, sp.ProjectID, sp.Name, sp.Goal, string(sp.State), sp.StartDate, sp.EndDate,
		sp.CreatedAt.Format(time.RFC3339))
	return err
}

func scanSprint(row scanner) (*model.Sprint, error) {
	var sp model.Sprint
	var state, createdStr string
	err := row.Scan(&sp.ID, &sp.ProjectID, &sp.Name, &sp.Goal, &state, &sp.StartDate, &sp.EndDate, &createdStr)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sp.State = model.SprintState(state)
	sp.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
	return &sp, nil
}

func (s *Store) GetSprint(sprintID string) (*model.Sprint, error) {
	row := s.db.QueryRow(`SELECT id, project_id, name, goal, state, start_date, end_date, created_at FROM sprints WHERE id = ?`, sprintID)
	return scanSprint(row)
}

func (s *Store) ListSprints(projectID string) ([]*model.Sprint, error) {
	query := `SELECT id, project_id, name, goal, state, start_date, end_date, created_at FROM sprints`
	var args []any
	if projectID != "" {
		query += " WHERE project_id = ?"
		args = append(args, projectID)
	}
	query += " ORDER BY created_at DESC"
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sprints []*model.Sprint
	for rows.Next() {
		sp, err := scanSprint(rows)
		if err != nil {
			return nil, err
		}
		sprints = append(sprints, sp)
	}
	return sprints, rows.Err()
}

func (s *Store) UpdateSprint(sprintID string, updates map[string]any) (*model.Sprint, error) {
	if _, err := s.GetSprint(sprintID); err != nil {
		return nil, err
	}
	var sets []string
	var args []any
	for k, v := range updates {
		switch k {
		case "name", "goal", "state", "start_date", "end_date":
			sets = append(sets, k+" = ?")
			args = append(args, v)
		}
	}
	if len(sets) == 0 {
		return s.GetSprint(sprintID)
	}
	args = append(args, sprintID)
	_, err := s.db.Exec("UPDATE sprints SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...)
	if err != nil {
		return nil, err
	}
	return s.GetSprint(sprintID)
}

func (s *Store) SetSprintState(sprintID string, state model.SprintState) error {
	sp, err := s.GetSprint(sprintID)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if state == model.SprintActive {
		if _, err := tx.Exec(
			"UPDATE sprints SET state = 'closed' WHERE project_id = ? AND state = 'active' AND id != ?",
			sp.ProjectID, sprintID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec("UPDATE sprints SET state = ? WHERE id = ?", string(state), sprintID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteSprint(sprintID string) error {
	if _, err := s.GetSprint(sprintID); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	s.db.Exec("UPDATE tasks SET sprint_id = '', updated_at = ? WHERE sprint_id = ?", now, sprintID)
	_, err := s.db.Exec("DELETE FROM sprints WHERE id = ?", sprintID)
	return err
}

// Settings

func (s *Store) GetSetting(key string) (string, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func (s *Store) GetAllSettings() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key, value FROM settings ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	settings := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		settings[key] = value
	}
	return settings, rows.Err()
}

// --- Burndown ---

type PointDay struct {
	Date      string `json:"date"`
	Remaining int    `json:"remaining"`
}

func (s *Store) SprintBurndown(sprintID string) ([]PointDay, error) {
	sp, err := s.GetSprint(sprintID)
	if err != nil {
		return nil, err
	}
	startStr := sp.StartDate
	if startStr == "" {
		startStr = sp.CreatedAt.Format(time.RFC3339)
	}
	start, err := time.Parse(time.RFC3339, startStr)
	if err != nil {
		start = sp.CreatedAt
	}
	start = start.UTC().Truncate(24 * time.Hour)
	end := time.Now().UTC().Truncate(24 * time.Hour)
	if sp.EndDate != "" {
		if e, err := time.Parse(time.RFC3339, sp.EndDate); err == nil {
			ed := e.UTC().Truncate(24 * time.Hour)
			if ed.Before(end) {
				end = ed
			}
		}
	}
	// Load sprint tasks: points + done-date (empty if not done).
	rows, err := s.db.Query(
		`SELECT story_points, status, DATE(updated_at) FROM tasks WHERE sprint_id = ?`, sprintID)
	if err != nil {
		return nil, err
	}
	type tp struct {
		points   int
		doneDate string // "" if not done
	}
	var tasks []tp
	for rows.Next() {
		var pts int
		var status, upd string
		rows.Scan(&pts, &status, &upd)
		d := ""
		if status == string(model.TaskDone) {
			d = upd
		}
		tasks = append(tasks, tp{points: pts, doneDate: d})
	}
	rows.Close()

	var days []PointDay
	for d := start; !d.After(end); d = d.Add(24 * time.Hour) {
		dayStr := d.Format("2006-01-02")
		remaining := 0
		for _, tk := range tasks {
			if tk.doneDate != "" && tk.doneDate <= dayStr {
				continue // completed on or before this day
			}
			remaining += tk.points
		}
		days = append(days, PointDay{Date: dayStr, Remaining: remaining})
	}
	return days, nil
}

